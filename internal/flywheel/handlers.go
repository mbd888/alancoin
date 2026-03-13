package flywheel

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for flywheel observability.
type Handler struct {
	engine     *Engine
	incentives *IncentiveEngine
	store      SnapshotStore
}

// NewHandler creates a new flywheel handler.
func NewHandler(engine *Engine, incentives *IncentiveEngine) *Handler {
	return &Handler{
		engine:     engine,
		incentives: incentives,
	}
}

// WithStore enables persisted history in the history endpoint.
func (h *Handler) WithStore(store SnapshotStore) *Handler {
	h.store = store
	return h
}

// RegisterRoutes sets up flywheel endpoints.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/flywheel/health", h.GetHealth)
	r.GET("/flywheel/state", h.GetState)
	r.GET("/flywheel/history", h.GetHistory)
	r.GET("/flywheel/incentives", h.GetIncentives)
}

// GetHealth returns the flywheel health score and tier.
// GET /v1/flywheel/health
func (h *Handler) GetHealth(c *gin.Context) {
	state := h.engine.Latest()
	if state == nil {
		c.JSON(http.StatusOK, gin.H{
			"healthScore": 0,
			"healthTier":  TierCold,
			"message":     "No flywheel data computed yet",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"healthScore": state.HealthScore,
		"healthTier":  state.HealthTier,
		"subScores": gin.H{
			"velocity":      state.VelocityScore,
			"growth":        state.GrowthScore,
			"density":       state.DensityScore,
			"effectiveness": state.EffectivenessScore,
			"retention":     state.RetentionScore,
		},
		"computedAt": state.ComputedAt,
	})
}

// GetState returns the full flywheel state.
// GET /v1/flywheel/state
func (h *Handler) GetState(c *gin.Context) {
	state := h.engine.Latest()
	if state == nil {
		c.JSON(http.StatusOK, gin.H{
			"state":   nil,
			"message": "No flywheel data computed yet",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"state": state})
}

// GetHistory returns the flywheel state over time.
// If a snapshot store is configured, serves persisted history (survives restarts).
// Otherwise falls back to the in-memory rolling window.
// GET /v1/flywheel/history
func (h *Handler) GetHistory(c *gin.Context) {
	var history []*State
	if h.store != nil {
		stored, err := h.store.Recent(c.Request.Context(), historySize)
		if err == nil && len(stored) > 0 {
			history = stored
		}
	}
	if len(history) == 0 {
		history = h.engine.History()
	}

	// Return condensed view: just health scores over time
	type point struct {
		HealthScore float64 `json:"healthScore"`
		HealthTier  string  `json:"healthTier"`
		TxPerHour   float64 `json:"txPerHour"`
		Agents      int     `json:"agents"`
		ComputedAt  string  `json:"computedAt"`
	}

	points := make([]point, 0, len(history))
	for _, s := range history {
		points = append(points, point{
			HealthScore: s.HealthScore,
			HealthTier:  s.HealthTier,
			TxPerHour:   s.TransactionsPerHour,
			Agents:      s.TotalAgents,
			ComputedAt:  s.ComputedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"history": points,
		"count":   len(points),
	})
}

// GetIncentives returns the current incentive schedule.
// GET /v1/flywheel/incentives
func (h *Handler) GetIncentives(c *gin.Context) {
	if h.incentives == nil {
		c.JSON(http.StatusOK, gin.H{
			"message": "Incentive engine not configured",
		})
		return
	}

	c.JSON(http.StatusOK, h.incentives.IncentiveSummary())
}
