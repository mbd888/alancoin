package reputation

import (
	"net/http"
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
	r.POST("/reputation/batch", h.GetBatchReputation)
	r.GET("/reputation/:address/history", h.GetReputationHistory)
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
