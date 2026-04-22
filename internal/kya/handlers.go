package kya

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/auth"
)

// Handler provides HTTP endpoints for KYA operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new KYA handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up read-only KYA routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/kya/certificates/:id", h.GetCertificate)
	r.GET("/kya/certificates/:id/verify", h.VerifyCertificate)
	r.GET("/kya/certificates/:id/compliance", h.ComplianceExport)
	r.GET("/kya/agents/:address", h.GetByAgent)
	r.GET("/kya/tenants/:tenantId/certificates", h.ListByTenant)
}

// RegisterProtectedRoutes sets up auth-required KYA routes.
func (h *Handler) RegisterProtectedRoutes(r *gin.RouterGroup) {
	r.POST("/kya/certificates", h.IssueCertificate)
	r.POST("/kya/certificates/:id/revoke", h.RevokeCertificate)
}

// IssueCertificate handles POST /v1/kya/certificates
func (h *Handler) IssueCertificate(c *gin.Context) {
	var req struct {
		AgentAddr   string          `json:"agentAddr" binding:"required"`
		Org         OrgBinding      `json:"org" binding:"required"`
		Permissions PermissionScope `json:"permissions"`
		ValidDays   int             `json:"validDays"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	if req.ValidDays <= 0 {
		req.ValidDays = 90
	}

	cert, err := h.service.Issue(c.Request.Context(), req.AgentAddr, req.Org, req.Permissions, req.ValidDays)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "issue_failed", "message": "Internal server error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"certificate": cert})
}

// GetCertificate handles GET /v1/kya/certificates/:id
func (h *Handler) GetCertificate(c *gin.Context) {
	cert, err := h.service.store.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"certificate": cert})
}

// VerifyCertificate handles GET /v1/kya/certificates/:id/verify
func (h *Handler) VerifyCertificate(c *gin.Context) {
	cert, err := h.service.Verify(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrCertNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{
			"valid":       false,
			"error":       err.Error(),
			"certificate": cert,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"valid": true, "certificate": cert})
}

// RevokeCertificate handles POST /v1/kya/certificates/:id/revoke
func (h *Handler) RevokeCertificate(c *gin.Context) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)

	if err := h.service.Revoke(c.Request.Context(), c.Param("id"), req.Reason); err != nil {
		if errors.Is(err, ErrCertNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke certificate"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

// ComplianceExport handles GET /v1/kya/certificates/:id/compliance
func (h *Handler) ComplianceExport(c *gin.Context) {
	report, err := h.service.ComplianceExport(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, ErrCertNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to export compliance report"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"report": report})
}

// GetByAgent handles GET /v1/kya/agents/:address
func (h *Handler) GetByAgent(c *gin.Context) {
	cert, err := h.service.store.GetByAgent(c.Request.Context(), c.Param("address"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"certificate": cert})
}

// ListByTenant handles GET /v1/kya/tenants/:tenantId/certificates
func (h *Handler) ListByTenant(c *gin.Context) {
	requestedTenant := c.Param("tenantId")
	callerTenant := auth.GetTenantID(c)
	if callerTenant != requestedTenant && !auth.IsAdminRequest(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "not your tenant"})
		return
	}

	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	certs, err := h.service.store.ListByTenant(c.Request.Context(), requestedTenant, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"certificates": certs, "count": len(certs)})
}
