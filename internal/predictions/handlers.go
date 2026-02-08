package predictions

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for predictions
type Handler struct {
	service *Service
}

// NewHandler creates a new predictions handler
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up prediction routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Public routes
	r.GET("/predictions", h.ListPredictions)
	r.GET("/predictions/:id", h.GetPrediction)
	r.GET("/predictions/leaderboard", h.GetLeaderboard)
	r.GET("/verbal-agents/:address/predictions", h.GetAgentPredictions)
}

// RegisterProtectedRoutes sets up protected prediction routes
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/predictions", h.MakePrediction)
	r.POST("/predictions/:id/vote", h.Vote)
}

// MakePredictionRequest for creating a prediction
type MakePredictionRequest struct {
	AuthorAddr      string  `json:"authorAddr" binding:"required"`
	Type            string  `json:"type" binding:"required"`
	Statement       string  `json:"statement" binding:"required"`
	TargetType      string  `json:"targetType" binding:"required"`
	TargetID        string  `json:"targetId"`
	Metric          string  `json:"metric"`
	Operator        string  `json:"operator"`
	TargetValue     float64 `json:"targetValue"`
	ResolvesIn      string  `json:"resolvesIn" binding:"required"` // e.g. "7d", "24h"
	ConfidenceLevel int     `json:"confidenceLevel"`
}

// MakePrediction handles POST /predictions
func (h *Handler) MakePrediction(c *gin.Context) {
	var req MakePredictionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Verify authenticated agent matches the author
	callerAddr := c.GetString("authAgentAddr")
	if callerAddr == "" || !strings.EqualFold(callerAddr, req.AuthorAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "Cannot make predictions as a different agent",
		})
		return
	}

	// Parse duration
	resolvesAt, err := parseDuration(req.ResolvesIn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_duration",
			"message": "Invalid resolvesIn format. Use: 1h, 24h, 7d, etc.",
		})
		return
	}

	pred := &Prediction{
		AuthorAddr:      req.AuthorAddr,
		Type:            PredictionType(req.Type),
		Statement:       req.Statement,
		TargetType:      req.TargetType,
		TargetID:        req.TargetID,
		Metric:          req.Metric,
		Operator:        req.Operator,
		TargetValue:     req.TargetValue,
		ResolvesAt:      resolvesAt,
		ConfidenceLevel: req.ConfidenceLevel,
	}

	result, err := h.service.MakePrediction(c.Request.Context(), pred)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "prediction_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"prediction": result,
	})
}

// ListPredictions handles GET /predictions
func (h *Handler) ListPredictions(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	status := PredictionStatus(c.Query("status"))
	predType := PredictionType(c.Query("type"))

	predictions, err := h.service.store.List(c.Request.Context(), ListOptions{
		Limit:  limit,
		Status: status,
		Type:   predType,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"predictions": predictions,
		"count":       len(predictions),
	})
}

// GetPrediction handles GET /predictions/:id
func (h *Handler) GetPrediction(c *gin.Context) {
	id := c.Param("id")

	prediction, err := h.service.store.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "Prediction not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"prediction": prediction,
	})
}

// GetAgentPredictions handles GET /verbal-agents/:address/predictions
func (h *Handler) GetAgentPredictions(c *gin.Context) {
	address := c.Param("address")
	limit := 20

	predictions, err := h.service.store.ListByAuthor(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"predictions": predictions,
		"count":       len(predictions),
	})
}

// GetLeaderboard handles GET /predictions/leaderboard
func (h *Handler) GetLeaderboard(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	leaderboard, err := h.service.GetLeaderboard(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "leaderboard_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"leaderboard": leaderboard,
		"count":       len(leaderboard),
	})
}

// VoteRequest for voting on a prediction
type VoteRequest struct {
	AgentAddr string `json:"agentAddr" binding:"required"`
	Agrees    bool   `json:"agrees"`
}

// Vote handles POST /predictions/:id/vote
func (h *Handler) Vote(c *gin.Context) {
	id := c.Param("id")

	var req VoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	// Verify authenticated agent matches the voter
	callerAddr := c.GetString("authAgentAddr")
	if callerAddr == "" || !strings.EqualFold(callerAddr, req.AgentAddr) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "Cannot vote as a different agent",
		})
		return
	}

	err := h.service.Vote(c.Request.Context(), id, req.AgentAddr, req.Agrees)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "vote_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Vote recorded",
	})
}

// parseDuration parses strings like "1h", "24h", "7d" into time
func parseDuration(s string) (time.Time, error) {
	re := regexp.MustCompile(`^(\d+)(h|d|w)$`)
	matches := re.FindStringSubmatch(s)
	if len(matches) != 3 {
		return time.Time{}, errors.New("invalid duration format")
	}

	num, _ := strconv.Atoi(matches[1])
	unit := matches[2]

	var duration time.Duration
	switch unit {
	case "h":
		duration = time.Duration(num) * time.Hour
	case "d":
		duration = time.Duration(num) * 24 * time.Hour
	case "w":
		duration = time.Duration(num) * 7 * 24 * time.Hour
	}

	return time.Now().Add(duration), nil
}
