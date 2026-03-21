package intelligence

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for intelligence profiles.
type Handler struct {
	store Store
}

// NewHandler creates a new intelligence handler.
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes sets up intelligence endpoints.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/intelligence/network/benchmarks", h.GetBenchmarks)
	r.GET("/intelligence/network/leaderboard", h.GetLeaderboard)
	r.POST("/intelligence/batch", h.BatchLookup)
	r.GET("/intelligence/:address", h.GetProfile)
	r.GET("/intelligence/:address/credit", h.GetCreditScore)
	r.GET("/intelligence/:address/risk", h.GetRiskScore)
	r.GET("/intelligence/:address/trends", h.GetTrends)
}

// GetProfile returns the full intelligence profile for an agent.
func (h *Handler) GetProfile(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	profile, err := h.store.GetProfile(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve intelligence profile",
		})
		return
	}

	if profile == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "No intelligence profile available for this agent",
		})
		return
	}

	c.JSON(http.StatusOK, profile)
}

// GetCreditScore returns the credit score and its factors.
func (h *Handler) GetCreditScore(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	profile, err := h.store.GetProfile(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve credit score",
		})
		return
	}

	if profile == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "No intelligence profile available for this agent",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"address":     profile.Address,
		"creditScore": profile.CreditScore,
		"tier":        profile.Tier,
		"factors":     profile.Credit,
		"computedAt":  profile.ComputedAt,
	})
}

// GetRiskScore returns the risk score and its factors.
func (h *Handler) GetRiskScore(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	profile, err := h.store.GetProfile(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve risk score",
		})
		return
	}

	if profile == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "No intelligence profile available for this agent",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"address":    profile.Address,
		"riskScore":  profile.RiskScore,
		"tier":       profile.Tier,
		"factors":    profile.Risk,
		"computedAt": profile.ComputedAt,
	})
}

// GetTrends returns historical score trends for an agent.
// Query params: from, to (RFC3339), limit (default 100, max 500).
func (h *Handler) GetTrends(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}

	to := time.Now().UTC()
	from := to.Add(-30 * 24 * time.Hour) // Default: last 30 days

	if f := c.Query("from"); f != "" {
		if parsed, err := time.Parse(time.RFC3339, f); err == nil {
			from = parsed
		}
	}
	if t := c.Query("to"); t != "" {
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			to = parsed
		}
	}

	history, err := h.store.GetScoreHistory(c.Request.Context(), address, from, to, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve score history",
		})
		return
	}

	if history == nil {
		history = []*ScoreHistoryPoint{}
	}

	c.JSON(http.StatusOK, gin.H{
		"address": address,
		"from":    from,
		"to":      to,
		"points":  history,
		"count":   len(history),
	})
}

// GetBenchmarks returns the latest network-wide benchmarks.
func (h *Handler) GetBenchmarks(c *gin.Context) {
	benchmarks, err := h.store.GetLatestBenchmarks(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve benchmarks",
		})
		return
	}

	if benchmarks == nil {
		c.JSON(http.StatusOK, gin.H{
			"message": "No benchmarks computed yet",
		})
		return
	}

	c.JSON(http.StatusOK, benchmarks)
}

// GetLeaderboard returns the top agents by composite score.
// Query param: limit (default 50, max 200).
func (h *Handler) GetLeaderboard(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}

	profiles, err := h.store.GetTopProfiles(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve leaderboard",
		})
		return
	}

	if profiles == nil {
		profiles = []*AgentProfile{}
	}

	c.JSON(http.StatusOK, gin.H{
		"agents": profiles,
		"count":  len(profiles),
	})
}

// batchRequest is the request body for BatchLookup.
type batchRequest struct {
	Addresses []string `json:"addresses"`
}

// BatchLookup returns profiles for multiple agents (max 100).
func (h *Handler) BatchLookup(c *gin.Context) {
	var req batchRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Request body must contain an 'addresses' array",
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
			"error":   "invalid_request",
			"message": "Maximum 100 addresses per batch request",
		})
		return
	}

	profiles, err := h.store.GetProfiles(c.Request.Context(), req.Addresses)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve profiles",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"profiles": profiles,
		"count":    len(profiles),
	})
}
