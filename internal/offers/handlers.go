package offers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/validation"
)

// Handler provides HTTP endpoints for the offers marketplace.
type Handler struct {
	service *Service
}

// NewHandler creates a new offers handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up read-only marketplace routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/offers", h.ListOffers)
	r.GET("/offers/:id", h.GetOffer)
	r.GET("/offers/:id/claims", h.ListClaims)
	r.GET("/claims/:id", h.GetClaim)
	r.GET("/agents/:address/offers", h.ListSellerOffers)
}

// RegisterProtectedRoutes sets up auth-required marketplace routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/offers", h.PostOffer)
	r.POST("/offers/:id/claim", h.ClaimOffer)
	r.POST("/offers/:id/cancel", h.CancelOffer)
	r.POST("/claims/:id/deliver", h.DeliverClaim)
	r.POST("/claims/:id/complete", h.CompleteClaim)
	r.POST("/claims/:id/refund", h.RefundClaim)
}

// PostOffer handles POST /v1/offers
func (h *Handler) PostOffer(c *gin.Context) {
	var req CreateOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "serviceType, price, and capacity are required",
		})
		return
	}

	if errs := validation.Validate(
		validation.ValidAmount("price", req.Price),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": errs.Error(),
			"details": errs,
		})
		return
	}

	if req.Capacity <= 0 || req.Capacity > MaxCapacity {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"message": fmt.Sprintf("capacity must be between 1 and %d", MaxCapacity),
		})
		return
	}

	callerAddr := c.GetString("authAgentAddr")
	offer, err := h.service.PostOffer(c.Request.Context(), callerAddr, req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrInvalidPrice) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": "offer_failed", "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"offer": offer})
}

// GetOffer handles GET /v1/offers/:id
func (h *Handler) GetOffer(c *gin.Context) {
	offer, err := h.service.GetOffer(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, ErrOfferNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Offer not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"offer": offer})
}

// ListOffers handles GET /v1/offers?service_type=inference&limit=50
func (h *Handler) ListOffers(c *gin.Context) {
	serviceType := c.Query("service_type")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	offers, err := h.service.ListOffers(c.Request.Context(), serviceType, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"offers": offers, "count": len(offers)})
}

// ListSellerOffers handles GET /v1/agents/:address/offers
func (h *Handler) ListSellerOffers(c *gin.Context) {
	address := c.Param("address")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	offers, err := h.service.ListOffersBySeller(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"offers": offers, "count": len(offers)})
}

// ClaimOffer handles POST /v1/offers/:id/claim
func (h *Handler) ClaimOffer(c *gin.Context) {
	offerID := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	claim, err := h.service.ClaimOffer(c.Request.Context(), offerID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "claim_failed"
		switch {
		case errors.Is(err, ErrOfferNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrOfferExpired):
			status = http.StatusGone
			code = "expired"
		case errors.Is(err, ErrOfferExhausted):
			status = http.StatusConflict
			code = "exhausted"
		case errors.Is(err, ErrOfferCancelled):
			status = http.StatusGone
			code = "cancelled"
		case errors.Is(err, ErrSelfClaim):
			status = http.StatusForbidden
			code = "self_claim"
		case errors.Is(err, ErrConditionNotMet):
			status = http.StatusForbidden
			code = "condition_not_met"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"claim": claim})
}

// CancelOffer handles POST /v1/offers/:id/cancel
func (h *Handler) CancelOffer(c *gin.Context) {
	offerID := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	offer, err := h.service.CancelOffer(c.Request.Context(), offerID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrOfferNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"offer": offer})
}

// ListClaims handles GET /v1/offers/:id/claims
func (h *Handler) ListClaims(c *gin.Context) {
	offerID := c.Param("id")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	claims, err := h.service.ListClaimsByOffer(c.Request.Context(), offerID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"claims": claims, "count": len(claims)})
}

// GetClaim handles GET /v1/claims/:id
func (h *Handler) GetClaim(c *gin.Context) {
	claim, err := h.service.GetClaim(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, ErrClaimNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Claim not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"claim": claim})
}

// DeliverClaim handles POST /v1/claims/:id/deliver
func (h *Handler) DeliverClaim(c *gin.Context) {
	claimID := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	claim, err := h.service.DeliverClaim(c.Request.Context(), claimID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrClaimNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrClaimNotPending):
			status = http.StatusConflict
			code = "not_pending"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"claim": claim})
}

// CompleteClaim handles POST /v1/claims/:id/complete
func (h *Handler) CompleteClaim(c *gin.Context) {
	claimID := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	claim, err := h.service.CompleteClaim(c.Request.Context(), claimID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrClaimNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrClaimNotPending):
			status = http.StatusConflict
			code = "not_pending"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"claim": claim})
}

// RefundClaim handles POST /v1/claims/:id/refund
func (h *Handler) RefundClaim(c *gin.Context) {
	claimID := c.Param("id")
	callerAddr := c.GetString("authAgentAddr")

	claim, err := h.service.RefundClaim(c.Request.Context(), claimID, callerAddr)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrClaimNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, ErrClaimNotPending):
			status = http.StatusConflict
			code = "not_pending"
		case errors.Is(err, ErrUnauthorized):
			status = http.StatusForbidden
			code = "unauthorized"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"claim": claim})
}
