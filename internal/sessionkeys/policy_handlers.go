package sessionkeys

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RegisterPolicyRoutes registers policy management routes on a protected router
// group. All routes require API key auth and ownership of the agent address.
func (h *Handler) RegisterPolicyRoutes(r *gin.RouterGroup) {
	// Policy CRUD
	r.POST("/agents/:address/policies", h.CreatePolicy)
	r.GET("/agents/:address/policies", h.ListPolicies)
	r.GET("/agents/:address/policies/:policyId", h.GetPolicy)
	r.PUT("/agents/:address/policies/:policyId", h.UpdatePolicy)
	r.DELETE("/agents/:address/policies/:policyId", h.DeletePolicy)

	// Attach / detach
	r.POST("/agents/:address/sessions/:keyId/policies/:policyId", h.AttachPolicy)
	r.DELETE("/agents/:address/sessions/:keyId/policies/:policyId", h.DetachPolicy)
	r.GET("/agents/:address/sessions/:keyId/policies", h.ListKeyPolicies)
}

// --- request types ---

type createPolicyRequest struct {
	Name  string `json:"name" binding:"required"`
	Rules []Rule `json:"rules" binding:"required"`
}

type updatePolicyRequest struct {
	Name  string `json:"name,omitempty"`
	Rules []Rule `json:"rules,omitempty"`
}

// --- handlers ---

// CreatePolicy handles POST /agents/:address/policies
func (h *Handler) CreatePolicy(c *gin.Context) {
	address := c.Param("address")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	var req createPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	if err := ValidateRules(req.Rules); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_rules", "message": err.Error()})
		return
	}

	policy := NewPolicy(req.Name, address, req.Rules)
	if err := ps.CreatePolicy(c.Request.Context(), policy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "creation_failed", "message": "Failed to create policy"})
		return
	}

	c.JSON(http.StatusCreated, policy)
}

// ListPolicies handles GET /agents/:address/policies
func (h *Handler) ListPolicies(c *gin.Context) {
	address := c.Param("address")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	policies, err := ps.ListPolicies(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": "Failed to list policies"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"policies": policies, "count": len(policies)})
}

// GetPolicy handles GET /agents/:address/policies/:policyId
func (h *Handler) GetPolicy(c *gin.Context) {
	policyID := c.Param("policyId")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	policy, err := ps.GetPolicy(c.Request.Context(), policyID)
	if err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get_failed", "message": "Failed to get policy"})
		return
	}

	// Verify ownership
	address := c.Param("address")
	if !strings.EqualFold(policy.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Policy does not belong to this agent"})
		return
	}

	c.JSON(http.StatusOK, policy)
}

// UpdatePolicy handles PUT /agents/:address/policies/:policyId
func (h *Handler) UpdatePolicy(c *gin.Context) {
	address := c.Param("address")
	policyID := c.Param("policyId")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	policy, err := ps.GetPolicy(c.Request.Context(), policyID)
	if err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get_failed", "message": "Failed to get policy"})
		return
	}

	if !strings.EqualFold(policy.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Policy does not belong to this agent"})
		return
	}

	var req updatePolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "Invalid request body"})
		return
	}

	if req.Name != "" {
		policy.Name = req.Name
	}
	if req.Rules != nil {
		if err := ValidateRules(req.Rules); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_rules", "message": err.Error()})
			return
		}
		policy.Rules = req.Rules
	}
	policy.UpdatedAt = time.Now()

	if err := ps.UpdatePolicy(c.Request.Context(), policy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed", "message": "Failed to update policy"})
		return
	}

	c.JSON(http.StatusOK, policy)
}

// DeletePolicy handles DELETE /agents/:address/policies/:policyId
func (h *Handler) DeletePolicy(c *gin.Context) {
	address := c.Param("address")
	policyID := c.Param("policyId")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	policy, err := ps.GetPolicy(c.Request.Context(), policyID)
	if err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "Policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get_failed", "message": "Failed to get policy"})
		return
	}

	if !strings.EqualFold(policy.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Policy does not belong to this agent"})
		return
	}

	if err := ps.DeletePolicy(c.Request.Context(), policyID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete_failed", "message": "Failed to delete policy"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Policy deleted", "policyId": policyID})
}

// AttachPolicy handles POST /agents/:address/sessions/:keyId/policies/:policyId
func (h *Handler) AttachPolicy(c *gin.Context) {
	address := c.Param("address")
	keyID := c.Param("keyId")
	policyID := c.Param("policyId")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	// Verify session key exists and belongs to agent
	key, err := h.manager.Get(c.Request.Context(), keyID)
	if err != nil {
		if err == ErrKeyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "session_not_found", "message": "Session key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	if !strings.EqualFold(key.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Session key does not belong to this agent"})
		return
	}

	// Verify policy exists and belongs to agent
	policy, err := ps.GetPolicy(c.Request.Context(), policyID)
	if err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "policy_not_found", "message": "Policy not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	if !strings.EqualFold(policy.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Policy does not belong to this agent"})
		return
	}

	att := &PolicyAttachment{
		SessionKeyID: keyID,
		PolicyID:     policyID,
		AttachedAt:   time.Now(),
		RuleState:    []byte(`{}`),
	}

	if err := ps.AttachPolicy(c.Request.Context(), att); err != nil {
		if err == ErrPolicyAlreadyExists {
			c.JSON(http.StatusConflict, gin.H{"error": "already_attached", "message": "Policy is already attached to this session key"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "attach_failed", "message": "Failed to attach policy"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Policy attached",
		"sessionKeyId": keyID,
		"policyId":     policyID,
	})
}

// DetachPolicy handles DELETE /agents/:address/sessions/:keyId/policies/:policyId
func (h *Handler) DetachPolicy(c *gin.Context) {
	address := c.Param("address")
	keyID := c.Param("keyId")
	policyID := c.Param("policyId")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	// Verify session key belongs to agent
	key, err := h.manager.Get(c.Request.Context(), keyID)
	if err != nil {
		if err == ErrKeyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "session_not_found", "message": "Session key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	if !strings.EqualFold(key.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Session key does not belong to this agent"})
		return
	}

	if err := ps.DetachPolicy(c.Request.Context(), keyID, policyID); err != nil {
		if err == ErrPolicyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_attached", "message": "Policy is not attached to this session key"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "detach_failed", "message": "Failed to detach policy"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Policy detached",
		"sessionKeyId": keyID,
		"policyId":     policyID,
	})
}

// ListKeyPolicies handles GET /agents/:address/sessions/:keyId/policies
func (h *Handler) ListKeyPolicies(c *gin.Context) {
	address := c.Param("address")
	keyID := c.Param("keyId")
	ps := h.manager.PolicyStore()
	if ps == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "policy_engine_disabled", "message": "Policy engine is not enabled"})
		return
	}

	// Verify session key belongs to agent
	key, err := h.manager.Get(c.Request.Context(), keyID)
	if err != nil {
		if err == ErrKeyNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "session_not_found", "message": "Session key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	if !strings.EqualFold(key.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "message": "Session key does not belong to this agent"})
		return
	}

	attachments, err := ps.GetAttachments(c.Request.Context(), keyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": "Failed to list attached policies"})
		return
	}

	// Enrich with policy details
	type enriched struct {
		Policy     *Policy           `json:"policy"`
		Attachment *PolicyAttachment `json:"attachment"`
	}

	var result []enriched
	for _, att := range attachments {
		p, err := ps.GetPolicy(c.Request.Context(), att.PolicyID)
		if err != nil {
			continue // policy deleted — skip
		}
		result = append(result, enriched{Policy: p, Attachment: att})
	}

	c.JSON(http.StatusOK, gin.H{"policies": result, "count": len(result)})
}
