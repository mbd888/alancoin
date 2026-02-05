package registry

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/logging"
)

// Handler provides HTTP handlers for the registry API
type Handler struct {
	store Store
}

// NewHandler creates a new registry handler
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
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

// RecordTransaction handles POST /transactions
// Records a transaction in the registry (for demo/testing)
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

	tx := &Transaction{
		TxHash:    req.TxHash,
		From:      req.From,
		To:        req.To,
		Amount:    req.Amount,
		ServiceID: req.ServiceID,
		Status:    "confirmed",
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
	active := true
	if c.Query("includeInactive") == "true" {
		active = false
	}
	query.Active = &active

	services, err := h.store.ListServices(ctx, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to list services",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"services": services,
		"count":    len(services),
		"query": gin.H{
			"type":     query.ServiceType,
			"minPrice": query.MinPrice,
			"maxPrice": query.MaxPrice,
		},
	})
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
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func parseIntQuery(c *gin.Context, key string, defaultVal int) int {
	if val := c.Query(key); val != "" {
		var i int
		if _, err := fmt.Sscanf(val, "%d", &i); err == nil && i > 0 {
			return i
		}
	}
	return defaultVal
}
