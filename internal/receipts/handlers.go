package receipts

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP endpoints for receipt operations.
type Handler struct {
	service *Service
}

// NewHandler creates a new receipt handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes sets up public (read-only) receipt routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/receipts/:id", h.GetReceipt)
	r.GET("/agents/:address/receipts", h.ListByAgent)
	r.POST("/receipts/verify", h.VerifyReceipt)

	r.GET("/chains/:scope/head", h.GetChainHead)
	r.GET("/chains/:scope/verify", h.VerifyChain)
	r.GET("/chains/:scope/bundle", h.ExportBundle)
	r.POST("/chains/bundle/verify", h.VerifyBundle)
}

// GetReceipt handles GET /v1/receipts/:id
func (h *Handler) GetReceipt(c *gin.Context) {
	id := c.Param("id")

	receipt, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Receipt not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"receipt": receipt})
}

// ListByAgent handles GET /v1/agents/:address/receipts
func (h *Handler) ListByAgent(c *gin.Context) {
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

	receipts, err := h.service.ListByAgent(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"receipts": receipts,
		"count":    len(receipts),
	})
}

// VerifyReceipt handles POST /v1/receipts/verify
func (h *Handler) VerifyReceipt(c *gin.Context) {
	var req VerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	resp, err := h.service.Verify(c.Request.Context(), req.ReceiptID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"verification": resp})
}

// GetChainHead handles GET /v1/chains/:scope/head
func (h *Handler) GetChainHead(c *gin.Context) {
	head, supported, err := h.service.GetChainHead(c.Request.Context(), c.Param("scope"))
	if !supported {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":   "not_supported",
			"message": "receipt store does not support chain operations",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"head": head})
}

// VerifyChain handles GET /v1/chains/:scope/verify?from=<idx>&to=<idx>
// from defaults to 0; to defaults to -1 (HEAD).
func (h *Handler) VerifyChain(c *gin.Context) {
	scope := c.Param("scope")
	lower := int64(0)
	upper := int64(-1)
	if v := c.Query("from"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed >= 0 {
			lower = parsed
		}
	}
	if v := c.Query("to"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			upper = parsed
		}
	}

	report, err := h.service.VerifyChain(c.Request.Context(), scope, lower, upper)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}
	status := http.StatusOK
	if report.Status != ChainIntact && report.Status != ChainEmpty {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"report": report})
}

// ExportBundle handles GET /v1/chains/:scope/bundle?since=<rfc3339>&until=<rfc3339>&format=<json|pdf>
// Returns the signed audit bundle as JSON by default, or as a one-page PDF
// summary when format=pdf. The PDF is a cover sheet for executive review;
// the JSON bundle remains the authoritative, byte-verifiable artifact.
func (h *Handler) ExportBundle(c *gin.Context) {
	scope := c.Param("scope")

	var since, until time.Time
	if v := c.Query("since"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_request",
				"message": "since must be RFC3339",
			})
			return
		}
		since = parsed
	}
	if v := c.Query("until"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_request",
				"message": "until must be RFC3339",
			})
			return
		}
		until = parsed
	}

	bundle, err := h.service.ExportBundle(c.Request.Context(), scope, since, until)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrSigningDisabled) {
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	switch c.Query("format") {
	case "pdf":
		pdfBytes, err := AuditBundleToPDF(bundle)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "pdf_generation_failed",
				"message": err.Error(),
			})
			return
		}
		filename := "alancoin-audit-" + scope + "-" + bundle.Manifest.GeneratedAt.UTC().Format("20060102T150405Z") + ".pdf"
		c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
		c.Data(http.StatusOK, "application/pdf", pdfBytes)
	default:
		c.JSON(http.StatusOK, bundle)
	}
}

// VerifyBundle handles POST /v1/chains/bundle/verify with an AuditBundle JSON body.
func (h *Handler) VerifyBundle(c *gin.Context) {
	var bundle AuditBundle
	if err := c.ShouldBindJSON(&bundle); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid bundle body",
		})
		return
	}
	report, err := h.service.VerifyBundle(&bundle)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "verification_failed",
			"message": err.Error(),
		})
		return
	}
	status := http.StatusOK
	if report.Status != ChainIntact && report.Status != ChainEmpty {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"report": report})
}
