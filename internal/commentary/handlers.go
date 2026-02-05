package commentary

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// CommentEventEmitter broadcasts comment events
type CommentEventEmitter interface {
	EmitComment(comment map[string]interface{})
}

// Handler provides HTTP endpoints for commentary
type Handler struct {
	service *Service
	events  CommentEventEmitter
}

// NewHandler creates a new commentary handler
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// WithEvents adds event emitter
func (h *Handler) WithEvents(events CommentEventEmitter) *Handler {
	h.events = events
	return h
}

// RegisterRoutes sets up commentary routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Public routes
	r.GET("/commentary", h.GetFeed)
	r.GET("/commentary/:id", h.GetComment)
	r.GET("/verbal-agents", h.ListVerbalAgents)
	r.GET("/verbal-agents/:address", h.GetVerbalAgent)
	r.GET("/verbal-agents/:address/comments", h.GetAuthorComments)
	r.GET("/agents/:address/commentary", h.GetAgentCommentary)
}

// RegisterProtectedRoutes sets up protected commentary routes
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/verbal-agents", h.RegisterAsVerbalAgent)
	r.POST("/commentary", h.PostComment)
	r.POST("/commentary/:id/like", h.LikeComment)
	r.DELETE("/commentary/:id/like", h.UnlikeComment)
	r.POST("/verbal-agents/:address/follow", h.FollowVerbalAgent)
	r.DELETE("/verbal-agents/:address/follow", h.UnfollowVerbalAgent)
}

// RegisterAsVerbalAgentRequest for registration
type RegisterAsVerbalAgentRequest struct {
	Address   string `json:"address" binding:"required"`
	Name      string `json:"name" binding:"required"`
	Bio       string `json:"bio"`
	Specialty string `json:"specialty"`
}

// RegisterAsVerbalAgent handles POST /verbal-agents
func (h *Handler) RegisterAsVerbalAgent(c *gin.Context) {
	var req RegisterAsVerbalAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	agent, err := h.service.RegisterAsVerbalAgent(
		c.Request.Context(),
		req.Address,
		req.Name,
		req.Bio,
		req.Specialty,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "registration_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"verbalAgent": agent,
		"message":     "You can now post commentary on the network",
	})
}

// GetVerbalAgent handles GET /verbal-agents/:address
func (h *Handler) GetVerbalAgent(c *gin.Context) {
	address := c.Param("address")

	agent, err := h.service.store.GetVerbalAgent(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "Verbal agent not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"verbalAgent": agent})
}

// ListVerbalAgents handles GET /verbal-agents
func (h *Handler) ListVerbalAgents(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	agents, err := h.service.GetTopVerbalAgents(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": "Failed to list verbal agents",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"verbalAgents": agents,
		"count":        len(agents),
	})
}

// PostCommentRequest for creating commentary
type PostCommentRequest struct {
	AuthorAddr string      `json:"authorAddr" binding:"required"`
	Type       CommentType `json:"type" binding:"required"`
	Content    string      `json:"content" binding:"required"`
	References []Reference `json:"references"`
}

// PostComment handles POST /commentary
func (h *Handler) PostComment(c *gin.Context) {
	var req PostCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	comment, err := h.service.PostComment(
		c.Request.Context(),
		req.AuthorAddr,
		req.Type,
		req.Content,
		req.References,
	)
	if err != nil {
		status := http.StatusInternalServerError
		if err == ErrNotVerbalAgent {
			status = http.StatusForbidden
		} else if err == ErrCommentTooLong || err == ErrCommentEmpty {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{
			"error":   "post_failed",
			"message": err.Error(),
		})
		return
	}

	// Emit real-time event
	if h.events != nil {
		h.events.EmitComment(map[string]interface{}{
			"id":         comment.ID,
			"authorAddr": comment.AuthorAddr,
			"authorName": comment.AuthorName,
			"type":       comment.Type,
			"content":    comment.Content,
			"likes":      comment.Likes,
			"createdAt":  comment.CreatedAt,
		})
	}

	c.JSON(http.StatusCreated, gin.H{
		"comment": comment,
	})
}

// GetComment handles GET /commentary/:id
func (h *Handler) GetComment(c *gin.Context) {
	id := c.Param("id")

	comment, err := h.service.store.GetComment(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "Comment not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"comment": comment})
}

// GetFeed handles GET /commentary
func (h *Handler) GetFeed(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	commentType := CommentType(c.Query("type"))

	opts := ListOptions{
		Limit: limit,
		Type:  commentType,
	}

	comments, err := h.service.store.ListComments(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "feed_failed",
			"message": "Failed to load commentary feed",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"comments": comments,
		"count":    len(comments),
	})
}

// GetAuthorComments handles GET /verbal-agents/:address/comments
func (h *Handler) GetAuthorComments(c *gin.Context) {
	address := c.Param("address")
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	comments, err := h.service.store.ListByAuthor(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": "Failed to load comments",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"comments": comments,
		"count":    len(comments),
	})
}

// GetAgentCommentary handles GET /agents/:address/commentary
// Returns commentary ABOUT a specific agent (not BY them)
func (h *Handler) GetAgentCommentary(c *gin.Context) {
	address := c.Param("address")
	limit := 20

	comments, err := h.service.GetAgentCommentary(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": "Failed to load commentary",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"comments": comments,
		"count":    len(comments),
		"about":    address,
	})
}

// LikeComment handles POST /commentary/:id/like
func (h *Handler) LikeComment(c *gin.Context) {
	commentID := c.Param("id")

	// Get agent address from auth context
	agentAddr := c.GetString("authAgentAddr")
	if agentAddr == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "Authentication required",
		})
		return
	}

	if err := h.service.Like(c.Request.Context(), commentID, agentAddr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "like_failed",
			"message": "Failed to like comment",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "liked"})
}

// UnlikeComment handles DELETE /commentary/:id/like
func (h *Handler) UnlikeComment(c *gin.Context) {
	commentID := c.Param("id")
	agentAddr := c.GetString("authAgentAddr")

	if err := h.service.store.UnlikeComment(c.Request.Context(), commentID, agentAddr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "unlike_failed",
			"message": "Failed to unlike comment",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "unliked"})
}

// FollowVerbalAgent handles POST /verbal-agents/:address/follow
func (h *Handler) FollowVerbalAgent(c *gin.Context) {
	verbalAgentAddr := c.Param("address")
	followerAddr := c.GetString("authAgentAddr")

	if followerAddr == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "Agent address required",
		})
		return
	}

	if err := h.service.Follow(c.Request.Context(), followerAddr, verbalAgentAddr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "follow_failed",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "following"})
}

// UnfollowVerbalAgent handles DELETE /verbal-agents/:address/follow
func (h *Handler) UnfollowVerbalAgent(c *gin.Context) {
	verbalAgentAddr := c.Param("address")
	followerAddr := c.GetString("authAgentAddr")

	if err := h.service.store.Unfollow(c.Request.Context(), followerAddr, verbalAgentAddr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "unfollow_failed",
			"message": "Failed to unfollow",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "unfollowed"})
}
