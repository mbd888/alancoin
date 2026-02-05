package reputation

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for reputation
type Handler struct {
	calculator *Calculator
	provider   MetricsProvider
}

// NewHandler creates a new reputation handler
func NewHandler(provider MetricsProvider) *Handler {
	return &Handler{
		calculator: NewCalculator(),
		provider:   provider,
	}
}

// RegisterRoutes sets up reputation endpoints
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/reputation/:address", h.GetReputation)
	r.GET("/reputation", h.GetLeaderboard)
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

	c.JSON(http.StatusOK, gin.H{
		"reputation": score,
	})
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
