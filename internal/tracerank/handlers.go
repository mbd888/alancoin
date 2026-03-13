package tracerank

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for TraceRank scores.
type Handler struct {
	store Store
}

// NewHandler creates a new TraceRank handler.
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes sets up TraceRank endpoints.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/tracerank/leaderboard", h.GetLeaderboard)
	r.GET("/tracerank/runs", h.GetRunHistory)
	r.GET("/tracerank/:address", h.GetScore)
}

// GetScore returns the TraceRank score for a single agent.
func (h *Handler) GetScore(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	score, err := h.store.GetScore(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve TraceRank score",
		})
		return
	}

	if score == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "No TraceRank score available for this agent",
		})
		return
	}

	c.JSON(http.StatusOK, score)
}

// GetLeaderboard returns the top N agents by TraceRank score.
// Default limit is 50, max is 200.
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

	scores, err := h.store.GetTopScores(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve leaderboard",
		})
		return
	}

	if scores == nil {
		scores = []*AgentScore{}
	}

	c.JSON(http.StatusOK, gin.H{
		"agents": scores,
		"count":  len(scores),
	})
}

// GetRunHistory returns recent TraceRank computation runs.
// Default limit is 10, max is 50.
func (h *Handler) GetRunHistory(c *gin.Context) {
	limit := 10
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 50 {
		limit = 50
	}

	runs, err := h.store.GetRunHistory(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve run history",
		})
		return
	}

	if runs == nil {
		runs = []*RunMetadata{}
	}

	c.JSON(http.StatusOK, gin.H{
		"runs":  runs,
		"count": len(runs),
	})
}
