package reputation

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for reputation
type Handler struct {
	calculator    *Calculator
	provider      MetricsProvider
	snapshotStore SnapshotStore
	signer        *Signer
}

// NewHandler creates a new reputation handler
func NewHandler(provider MetricsProvider) *Handler {
	return &Handler{
		calculator: NewCalculator(),
		provider:   provider,
	}
}

// NewHandlerFull creates a handler with snapshot store and signer.
func NewHandlerFull(provider MetricsProvider, store SnapshotStore, signer *Signer) *Handler {
	return &Handler{
		calculator:    NewCalculator(),
		provider:      provider,
		snapshotStore: store,
		signer:        signer,
	}
}

// RegisterRoutes sets up reputation endpoints
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/reputation/:address", h.GetReputation)
	r.GET("/reputation", h.GetLeaderboard)
	r.POST("/reputation/batch", h.GetBatchReputation)
	r.GET("/reputation/:address/history", h.GetReputationHistory)
	r.POST("/reputation/compare", h.CompareAgents)
}

// GetReputation returns reputation score for a single agent
func (h *Handler) GetReputation(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	metrics, err := h.provider.GetAgentMetrics(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "agent_not_found",
			"message": "Agent not found or has no transaction history",
		})
		return
	}

	score := h.calculator.Calculate(address, *metrics)

	resp := gin.H{"reputation": score}
	if h.signer != nil {
		sig, issued, expires, err := h.signer.Sign(score)
		if err == nil {
			resp["signature"] = sig
			resp["issuedAt"] = issued
			resp["expiresAt"] = expires
		}
	}
	c.JSON(http.StatusOK, resp)
}

// LeaderboardEntry is a single entry in the reputation leaderboard
type LeaderboardEntry struct {
	Rank       int     `json:"rank"`
	Address    string  `json:"address"`
	Score      float64 `json:"score"`
	Tier       Tier    `json:"tier"`
	TotalTxns  int     `json:"totalTransactions"`
	VolumeUSD  float64 `json:"totalVolumeUsd"`
	DaysActive int     `json:"daysOnNetwork"`
}

// GetLeaderboard returns top agents by reputation
func (h *Handler) GetLeaderboard(c *gin.Context) {
	// Parse query params
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 100 {
				limit = 100
			}
		}
	}

	minScore := 0.0
	if m := c.Query("minScore"); m != "" {
		if parsed, err := strconv.ParseFloat(m, 64); err == nil {
			minScore = parsed
		}
	}

	tierFilter := Tier(c.Query("tier"))

	// Get all metrics
	allMetrics, err := h.provider.GetAllAgentMetrics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "calculation_failed",
			"message": "Failed to calculate reputation scores",
		})
		return
	}

	// Calculate scores
	var entries []LeaderboardEntry
	for address, metrics := range allMetrics {
		score := h.calculator.Calculate(address, *metrics)

		// Apply filters
		if score.Score < minScore {
			continue
		}
		if tierFilter != "" && score.Tier != tierFilter {
			continue
		}

		entries = append(entries, LeaderboardEntry{
			Address:    address,
			Score:      score.Score,
			Tier:       score.Tier,
			TotalTxns:  metrics.TotalTransactions,
			VolumeUSD:  metrics.TotalVolumeUSD,
			DaysActive: metrics.DaysOnNetwork,
		})
	}

	// Sort by score descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Score > entries[j].Score
	})

	// Assign ranks and limit
	if len(entries) > limit {
		entries = entries[:limit]
	}
	for i := range entries {
		entries[i].Rank = i + 1
	}

	// Tier distribution stats
	tierCounts := map[Tier]int{
		TierNew:         0,
		TierEmerging:    0,
		TierEstablished: 0,
		TierTrusted:     0,
		TierElite:       0,
	}
	for _, m := range allMetrics {
		score := h.calculator.Calculate("", *m)
		tierCounts[score.Tier]++
	}

	c.JSON(http.StatusOK, gin.H{
		"leaderboard": entries,
		"total":       len(allMetrics),
		"tiers": gin.H{
			"new":         tierCounts[TierNew],
			"emerging":    tierCounts[TierEmerging],
			"established": tierCounts[TierEstablished],
			"trusted":     tierCounts[TierTrusted],
			"elite":       tierCounts[TierElite],
		},
	})
}

// GetBatchReputation returns reputation scores for multiple agents.
// POST /v1/reputation/batch
func (h *Handler) GetBatchReputation(c *gin.Context) {
	var req BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Request body must contain 'addresses' array",
		})
		return
	}

	if len(req.Addresses) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "At least one address is required",
		})
		return
	}
	if len(req.Addresses) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "too_many_addresses",
			"message": "Maximum 100 addresses per batch request",
		})
		return
	}

	var scores []*SignedScore
	for _, addr := range req.Addresses {
		addr = strings.ToLower(addr)
		metrics, err := h.provider.GetAgentMetrics(c.Request.Context(), addr)
		if err != nil {
			// Include a nil/zero score for unknown agents
			scores = append(scores, &SignedScore{
				Reputation: &Score{Address: addr, Score: 0, Tier: TierNew},
			})
			continue
		}
		score := h.calculator.Calculate(addr, *metrics)
		signed := &SignedScore{Reputation: score}
		if h.signer != nil {
			sig, issued, expires, err := h.signer.Sign(score)
			if err == nil {
				signed.Signature = sig
				signed.IssuedAt = issued
				signed.ExpiresAt = expires
			}
		}
		scores = append(scores, signed)
	}

	resp := BatchResponse{Scores: scores}
	if h.signer != nil {
		sig, issued, expires, err := h.signer.Sign(scores)
		if err == nil {
			resp.Signature = sig
			resp.IssuedAt = issued
			resp.ExpiresAt = expires
		}
	}

	c.JSON(http.StatusOK, resp)
}

// GetReputationHistory returns historical reputation snapshots.
// GET /v1/reputation/:address/history?from=&to=&limit=
func (h *Handler) GetReputationHistory(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	if h.snapshotStore == nil {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":   "not_available",
			"message": "Historical reputation data is not available",
		})
		return
	}

	q := HistoryQuery{
		Address: address,
		Limit:   100,
	}

	if from := c.Query("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			q.From = t
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			q.To = t
		}
	}
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			q.Limit = parsed
			if q.Limit > 1000 {
				q.Limit = 1000
			}
		}
	}

	snapshots, err := h.snapshotStore.Query(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "query_failed",
			"message": "Failed to query reputation history",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"address":   address,
		"snapshots": snapshots,
		"count":     len(snapshots),
	})
}

// CompareAgents returns side-by-side reputation comparison.
// POST /v1/reputation/compare
func (h *Handler) CompareAgents(c *gin.Context) {
	var req CompareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Request body must contain 'addresses' array",
		})
		return
	}

	if len(req.Addresses) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "At least 2 addresses are required for comparison",
		})
		return
	}
	if len(req.Addresses) > 10 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "too_many_addresses",
			"message": "Maximum 10 addresses per comparison",
		})
		return
	}

	var agents []*Score
	bestAddr := ""
	bestScore := -1.0

	for _, addr := range req.Addresses {
		addr = strings.ToLower(addr)
		metrics, err := h.provider.GetAgentMetrics(c.Request.Context(), addr)
		if err != nil {
			agents = append(agents, &Score{Address: addr, Score: 0, Tier: TierNew})
			continue
		}
		score := h.calculator.Calculate(addr, *metrics)
		agents = append(agents, score)
		if score.Score > bestScore {
			bestScore = score.Score
			bestAddr = addr
		}
	}

	resp := CompareResponse{
		Agents: agents,
		Best:   bestAddr,
	}
	if h.signer != nil {
		sig, issued, expires, err := h.signer.Sign(agents)
		if err == nil {
			resp.Signature = sig
			resp.IssuedAt = issued
			resp.ExpiresAt = expires
		}
	}

	c.JSON(http.StatusOK, resp)
}
