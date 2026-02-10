package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/logging"
	"github.com/mbd888/alancoin/internal/validation"
)

// ReputationProvider supplies reputation scores for agents.
// This decouples the registry from the reputation package.
type ReputationProvider interface {
	GetScore(ctx context.Context, address string) (score float64, tier string, err error)
}

// Handler provides HTTP handlers for the registry API
type Handler struct {
	store      Store
	verifier   TxVerifier         // optional on-chain verifier
	reputation ReputationProvider // optional reputation enrichment
}

// NewHandler creates a new registry handler
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// SetReputation attaches a reputation provider for discovery enrichment
func (h *Handler) SetReputation(r ReputationProvider) {
	h.reputation = r
}

// RegisterRoutes sets up the registry routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Agent management
	r.POST("/agents", h.RegisterAgent)
	r.GET("/agents", h.ListAgents)
	r.GET("/agents/:address", h.GetAgent)
	r.DELETE("/agents/:address", h.DeleteAgent)

	// Service management
	r.POST("/agents/:address/services", h.AddService)
	r.PUT("/agents/:address/services/:serviceId", h.UpdateService)
	r.DELETE("/agents/:address/services/:serviceId", h.RemoveService)

	// Discovery (the key feature)
	r.GET("/services", h.DiscoverServices)

	// Transactions
	r.GET("/agents/:address/transactions", h.ListTransactions)

	// Network stats
	r.GET("/network/stats", h.GetNetworkStats)

	// PUBLIC FEED - The viral content layer (Moltbook playbook)
	r.GET("/feed", h.GetPublicFeed)

	// Record transaction (internal - for demo and testing)
	// In production, this would be called automatically when payments are verified
	r.POST("/transactions", h.RecordTransaction)
}

// -----------------------------------------------------------------------------
// Transaction Recording Handler
// -----------------------------------------------------------------------------

// RecordTransactionRequest is the payload for recording a transaction
type RecordTransactionRequest struct {
	TxHash    string `json:"txHash" binding:"required"`
	From      string `json:"from" binding:"required"`
	To        string `json:"to" binding:"required"`
	Amount    string `json:"amount" binding:"required"`
	ServiceID string `json:"serviceId,omitempty"`
}

// TxVerifier verifies on-chain transaction receipts
type TxVerifier interface {
	VerifyPayment(ctx context.Context, from string, minAmount string, txHash string) (bool, error)
}

// SetVerifier attaches a transaction verifier to the handler
func (h *Handler) SetVerifier(v TxVerifier) {
	h.verifier = v
}

// RecordTransaction handles POST /transactions
// Records a transaction in the registry after optional on-chain verification.
func (h *Handler) RecordTransaction(c *gin.Context) {
	ctx := c.Request.Context()
	logger := logging.L(ctx)

	var req RecordTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate addresses and amount
	if errs := validation.Validate(
		validation.ValidAddress("from", req.From),
		validation.ValidAddress("to", req.To),
		validation.ValidAmount("amount", req.Amount),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_failed",
			"message": errs.Error(),
		})
		return
	}

	// Reject self-trade (prevents reputation gaming)
	if strings.EqualFold(req.From, req.To) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "self_trade",
			"message": "Sender and recipient cannot be the same address",
		})
		return
	}

	// Verify the authenticated caller is the sender (prevents forged transactions)
	if callerAddr, ok := c.Get("authAgentAddr"); ok {
		if addr, isStr := callerAddr.(string); isStr && !strings.EqualFold(addr, req.From) {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "forbidden",
				"message": "Cannot record transactions for another agent's address",
			})
			return
		}
	}

	status := "confirmed" // Default to confirmed in demo/in-memory mode

	// Verify on-chain if verifier is available
	if h.verifier != nil {
		status = "pending"
		verified, err := h.verifier.VerifyPayment(ctx, req.From, req.Amount, req.TxHash)
		if err != nil {
			logger.Warn("on-chain verification failed, recording as pending",
				"tx_hash", req.TxHash,
				"error", err,
			)
		} else if verified {
			status = "confirmed"
		} else {
			status = "failed"
		}
	}

	tx := &Transaction{
		TxHash:    req.TxHash,
		From:      req.From,
		To:        req.To,
		Amount:    req.Amount,
		ServiceID: req.ServiceID,
		Status:    status,
	}

	if err := h.store.RecordTransaction(ctx, tx); err != nil {
		logger.Error("failed to record transaction", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to record transaction",
		})
		return
	}

	logger.Info("transaction recorded",
		"tx_hash", tx.TxHash,
		"from", tx.From,
		"to", tx.To,
		"amount", tx.Amount,
		"status", tx.Status,
	)

	c.JSON(http.StatusCreated, tx)
}

// -----------------------------------------------------------------------------
// Agent Handlers
// -----------------------------------------------------------------------------

// RegisterAgent handles POST /agents
func (h *Handler) RegisterAgent(c *gin.Context) {
	ctx := c.Request.Context()
	logger := logging.L(ctx)

	var req RegisterAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate address format (basic check)
	if !isValidAddress(req.Address) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_address",
			"message": "Address must be a valid Ethereum address",
		})
		return
	}

	agent := &Agent{
		Address:      req.Address,
		Name:         req.Name,
		Description:  req.Description,
		OwnerAddress: req.OwnerAddress,
		Endpoint:     req.Endpoint,
		IsAutonomous: req.OwnerAddress != "",
	}

	if err := h.store.CreateAgent(ctx, agent); err != nil {
		if errors.Is(err, ErrAgentExists) {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "agent_exists",
				"message": "An agent with this address is already registered",
			})
			return
		}
		logger.Error("failed to create agent", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to register agent",
		})
		return
	}

	logger.Info("agent registered",
		"address", agent.Address,
		"name", agent.Name,
	)

	c.JSON(http.StatusCreated, agent)
}

// GetAgent handles GET /agents/:address
func (h *Handler) GetAgent(c *gin.Context) {
	ctx := c.Request.Context()
	address := c.Param("address")

	agent, err := h.store.GetAgent(ctx, address)
	if err != nil {
		if errors.Is(err, ErrAgentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Agent not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to get agent",
		})
		return
	}

	c.JSON(http.StatusOK, agent)
}

// ListAgents handles GET /agents
func (h *Handler) ListAgents(c *gin.Context) {
	ctx := c.Request.Context()

	query := AgentQuery{
		ServiceType: c.Query("serviceType"),
		Limit:       parseIntQuery(c, "limit", 100),
		Offset:      parseIntQuery(c, "offset", 0),
	}

	if activeStr := c.Query("active"); activeStr != "" {
		active := activeStr == "true"
		query.Active = &active
	}

	agents, err := h.store.ListAgents(ctx, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to list agents",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"agents": agents,
		"count":  len(agents),
	})
}

// DeleteAgent handles DELETE /agents/:address
func (h *Handler) DeleteAgent(c *gin.Context) {
	ctx := c.Request.Context()
	logger := logging.L(ctx)
	address := c.Param("address")

	// Auth handled at route level via auth.RequireOwnership middleware

	if err := h.store.DeleteAgent(ctx, address); err != nil {
		if errors.Is(err, ErrAgentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Agent not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to delete agent",
		})
		return
	}

	logger.Info("agent deleted", "address", address)
	c.Status(http.StatusNoContent)
}

// -----------------------------------------------------------------------------
// Service Handlers
// -----------------------------------------------------------------------------

// AddService handles POST /agents/:address/services
func (h *Handler) AddService(c *gin.Context) {
	ctx := c.Request.Context()
	logger := logging.L(ctx)
	address := c.Param("address")

	var req AddServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate service type
	if !IsKnownServiceType(req.Type) {
		// Allow unknown types but log it
		logger.Warn("unknown service type", "type", req.Type)
	}

	// Validate price is a valid numeric amount (prevents CAST errors in discovery queries)
	if errs := validation.Validate(
		validation.ValidAmount("price", req.Price),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_failed",
			"message": errs.Error(),
		})
		return
	}

	service := &Service{
		Type:        req.Type,
		Name:        req.Name,
		Description: req.Description,
		Price:       req.Price,
		Endpoint:    req.Endpoint,
		Active:      true,
	}

	if err := h.store.AddService(ctx, address, service); err != nil {
		if errors.Is(err, ErrAgentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Agent not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to add service",
		})
		return
	}

	logger.Info("service added",
		"agent", address,
		"service_id", service.ID,
		"type", service.Type,
	)

	c.JSON(http.StatusCreated, service)
}

// UpdateService handles PUT /agents/:address/services/:serviceId
func (h *Handler) UpdateService(c *gin.Context) {
	ctx := c.Request.Context()
	address := c.Param("address")
	serviceID := c.Param("serviceId")

	var service Service
	if err := c.ShouldBindJSON(&service); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Validate price if provided
	if service.Price != "" {
		if vErr := validation.ValidAmount("price", service.Price)(); vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "validation_failed",
				"message": vErr.Field + ": " + vErr.Message,
			})
			return
		}
	}

	service.ID = serviceID

	if err := h.store.UpdateService(ctx, address, &service); err != nil {
		if errors.Is(err, ErrAgentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Agent not found",
			})
			return
		}
		if errors.Is(err, ErrServiceNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Service not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to update service",
		})
		return
	}

	c.JSON(http.StatusOK, service)
}

// RemoveService handles DELETE /agents/:address/services/:serviceId
func (h *Handler) RemoveService(c *gin.Context) {
	ctx := c.Request.Context()
	logger := logging.L(ctx)
	address := c.Param("address")
	serviceID := c.Param("serviceId")

	if err := h.store.RemoveService(ctx, address, serviceID); err != nil {
		if errors.Is(err, ErrAgentNotFound) || errors.Is(err, ErrServiceNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Agent or service not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to remove service",
		})
		return
	}

	logger.Info("service removed",
		"agent", address,
		"service_id", serviceID,
	)

	c.Status(http.StatusNoContent)
}

// -----------------------------------------------------------------------------
// Discovery Handler (the key feature)
// -----------------------------------------------------------------------------

// DiscoverServices handles GET /services
// This is how agents find each other
func (h *Handler) DiscoverServices(c *gin.Context) {
	ctx := c.Request.Context()

	query := AgentQuery{
		ServiceType: c.Query("type"),
		MinPrice:    c.Query("minPrice"),
		MaxPrice:    c.Query("maxPrice"),
		Limit:       parseIntQuery(c, "limit", 100),
		Offset:      parseIntQuery(c, "offset", 0),
	}

	// Default to active services only
	active := c.Query("includeInactive") != "true"
	query.Active = &active

	services, err := h.store.ListServices(ctx, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to list services",
		})
		return
	}

	// Enrich with reputation data
	h.enrichWithReputation(ctx, services)

	// Sort by requested strategy
	sortBy := c.DefaultQuery("sortBy", "price")
	h.sortServices(services, sortBy)

	c.JSON(http.StatusOK, gin.H{
		"services": services,
		"count":    len(services),
		"query": gin.H{
			"type":     query.ServiceType,
			"minPrice": query.MinPrice,
			"maxPrice": query.MaxPrice,
			"sortBy":   sortBy,
		},
	})
}

// enrichWithReputation adds reputation data to service listings.
// If the matview already provided reputation data (non-zero score or non-empty tier),
// this skips the per-agent lookups for that listing.
func (h *Handler) enrichWithReputation(ctx context.Context, services []ServiceListing) {
	if h.reputation == nil {
		return
	}

	// Cache scores by address to avoid duplicate lookups
	type repData struct {
		score float64
		tier  string
	}
	cache := make(map[string]*repData)

	for i := range services {
		// Skip if matview already populated reputation data
		if services[i].ReputationTier != "" && services[i].ReputationTier != "new" || services[i].ReputationScore > 0 || services[i].TxCount > 0 {
			continue
		}

		addr := services[i].AgentAddress
		if _, ok := cache[addr]; !ok {
			score, tier, err := h.reputation.GetScore(ctx, addr)
			if err != nil {
				cache[addr] = &repData{score: 0, tier: "new"}
			} else {
				cache[addr] = &repData{score: score, tier: tier}
			}
		}
		rd := cache[addr]
		services[i].ReputationScore = rd.score
		services[i].ReputationTier = rd.tier

		// Pull agent stats for success rate and tx count
		if agent, err := h.store.GetAgent(ctx, addr); err == nil {
			services[i].SuccessRate = agent.Stats.SuccessRate
			services[i].TxCount = agent.Stats.TransactionCount
		}
	}
}

// sortServices sorts listings by the requested strategy.
func (h *Handler) sortServices(services []ServiceListing, sortBy string) {
	switch sortBy {
	case "reputation":
		sort.Slice(services, func(i, j int) bool {
			return services[i].ReputationScore > services[j].ReputationScore
		})
	case "value":
		// Compute value score: reputation / price (higher = better deal)
		for i := range services {
			var priceF float64
			if p, err := fmt.Sscanf(services[i].Price, "%f", &priceF); p != 1 || err != nil || priceF <= 0 {
				// Unparseable price gets zero value score (not inflated)
				services[i].ValueScore = 0
				continue
			}
			services[i].ValueScore = services[i].ReputationScore / priceF
		}
		sort.Slice(services, func(i, j int) bool {
			return services[i].ValueScore > services[j].ValueScore
		})
	default: // "price" — already sorted by price from store
	}
}

// -----------------------------------------------------------------------------
// Transaction Handler
// -----------------------------------------------------------------------------

// ListTransactions handles GET /agents/:address/transactions
func (h *Handler) ListTransactions(c *gin.Context) {
	ctx := c.Request.Context()
	address := c.Param("address")
	limit := parseIntQuery(c, "limit", 100)

	txs, err := h.store.ListTransactions(ctx, address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to list transactions",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"transactions": txs,
		"count":        len(txs),
	})
}

// -----------------------------------------------------------------------------
// Stats Handler
// -----------------------------------------------------------------------------

// GetNetworkStats handles GET /network/stats
func (h *Handler) GetNetworkStats(c *gin.Context) {
	ctx := c.Request.Context()

	stats, err := h.store.GetNetworkStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to get network stats",
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// -----------------------------------------------------------------------------
// Public Feed Handler (The Moltbook playbook - transactions ARE the content)
// -----------------------------------------------------------------------------

// FeedItem is a human-readable transaction for the public feed
type FeedItem struct {
	ID          string `json:"id"`
	FromName    string `json:"fromName"`
	FromAddress string `json:"fromAddress"`
	ToName      string `json:"toName"`
	ToAddress   string `json:"toAddress"`
	Amount      string `json:"amount"`
	ServiceName string `json:"serviceName,omitempty"`
	ServiceType string `json:"serviceType,omitempty"`
	TxHash      string `json:"txHash"`
	Timestamp   string `json:"timestamp"`
	TimeAgo     string `json:"timeAgo"`
}

// GetPublicFeed handles GET /feed
// This is the viral content layer - shows agents hiring each other in real-time
// No auth required - this is meant to be screenshotted and shared
func (h *Handler) GetPublicFeed(c *gin.Context) {
	ctx := c.Request.Context()
	limit := parseIntQuery(c, "limit", 50)
	if limit > 100 {
		limit = 100 // Cap feed to prevent expensive agent lookups
	}

	// Get recent transactions across all agents
	txs, err := h.store.GetRecentTransactions(ctx, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to get feed",
		})
		return
	}

	// Enrich with agent names for readability
	feed := make([]FeedItem, 0, len(txs))
	for _, tx := range txs {
		item := FeedItem{
			ID:          tx.ID,
			FromAddress: tx.From,
			ToAddress:   tx.To,
			Amount:      tx.Amount,
			TxHash:      tx.TxHash,
			Timestamp:   tx.CreatedAt.Format("2006-01-02T15:04:05Z"),
			TimeAgo:     timeAgo(tx.CreatedAt),
		}

		// Try to get agent names (makes feed more readable)
		if fromAgent, err := h.store.GetAgent(ctx, tx.From); err == nil {
			item.FromName = fromAgent.Name
		} else {
			item.FromName = truncateAddress(tx.From)
		}

		if toAgent, err := h.store.GetAgent(ctx, tx.To); err == nil {
			item.ToName = toAgent.Name
			// Try to find service name
			for _, svc := range toAgent.Services {
				if svc.ID == tx.ServiceID {
					item.ServiceName = svc.Name
					item.ServiceType = svc.Type
					break
				}
			}
		} else {
			item.ToName = truncateAddress(tx.To)
		}

		feed = append(feed, item)
	}

	// Get network stats for context
	stats, _ := h.store.GetNetworkStats(ctx)

	c.JSON(http.StatusOK, gin.H{
		"feed": feed,
		"stats": gin.H{
			"totalAgents":       stats.TotalAgents,
			"totalTransactions": stats.TotalTransactions,
			"totalVolume":       stats.TotalVolume,
		},
		"message": "AI agents hiring each other in real-time",
	})
}

// timeAgo returns a human-readable time difference
func timeAgo(t time.Time) string {
	diff := time.Since(t)
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// truncateAddress shortens an address for display
func truncateAddress(addr string) string {
	if len(addr) <= 10 {
		return addr
	}
	return addr[:6] + "..." + addr[len(addr)-4:]
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func isValidAddress(addr string) bool {
	// Basic Ethereum address validation
	if !strings.HasPrefix(addr, "0x") {
		return false
	}
	if len(addr) != 42 {
		return false
	}
	// Check hex characters
	for _, c := range addr[2:] {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

func parseIntQuery(c *gin.Context, key string, defaultVal int) int {
	if val := c.Query(key); val != "" {
		var i int
		if _, err := fmt.Sscanf(val, "%d", &i); err == nil && i > 0 {
			if i > 1000 {
				i = 1000
			}
			return i
		}
	}
	return defaultVal
}
