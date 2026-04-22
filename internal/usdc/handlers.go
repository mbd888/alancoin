package usdc

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// PayoutHandler exposes admin-only endpoints for triggering outbound
// USDC transfers and inspecting their final state.
// Mount under a router group that has already applied admin auth.
type PayoutHandler struct {
	svc *PayoutService
}

// NewPayoutHandler returns a handler bound to the given service.
// Pass nil to intentionally disable the routes (the register call becomes a no-op).
func NewPayoutHandler(svc *PayoutService) *PayoutHandler {
	return &PayoutHandler{svc: svc}
}

// RegisterRoutes wires the payout routes onto the given (admin) group.
// When the handler has no service, it registers 503 stubs so callers can
// tell the difference between "endpoint absent" and "endpoint disabled".
func (h *PayoutHandler) RegisterRoutes(r *gin.RouterGroup) {
	if h == nil || h.svc == nil {
		r.POST("/admin/payouts", unavailableHandler)
		r.GET("/admin/payouts/:ref", unavailableHandler)
		return
	}
	r.POST("/admin/payouts", h.Send)
	r.GET("/admin/payouts/:ref", h.Get)
}

func unavailableHandler(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error":   "payouts_disabled",
		"message": "PAYOUTS_ENABLED is not set on this deployment",
	})
}

// sendPayoutReq is the POST body for Send. Amount is a decimal USDC string
// (e.g. "1.250000") rather than a raw integer so the API matches every
// other amount field in the system.
type sendPayoutReq struct {
	ClientRef string `json:"clientRef" binding:"required"`
	To        string `json:"to"        binding:"required"`
	Amount    string `json:"amount"    binding:"required"`
}

// Send handles POST /admin/payouts
// Blocks until the payout reaches a final state or the service-side
// receipt timeout elapses. Clients should budget for this — the typical
// path is 10-30s on L2s.
func (h *PayoutHandler) Send(c *gin.Context) {
	var req sendPayoutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": err.Error(),
		})
		return
	}
	amount, ok := Parse(req.Amount)
	if !ok || amount == nil || amount.Sign() <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_amount",
			"message": "amount must be a positive decimal USDC string",
		})
		return
	}

	payout, err := h.svc.Send(c.Request.Context(), TransferRequest{
		ChainID:   h.svc.chain.ID,
		ToAddr:    req.To,
		Amount:    amount,
		ClientRef: req.ClientRef,
	})
	if err != nil {
		// Classify into client vs server error so operators can tell them apart.
		if errors.Is(err, ErrAmountNonPositive) || errors.Is(err, ErrBadRecipient) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_request",
				"message": err.Error(),
			})
			return
		}
		if errors.Is(err, ErrChainMismatch) {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "chain_mismatch",
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "payout_failed",
			"message": "Payout failed",
			"payout":  payout,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"payout": toPayoutView(payout)})
}

// Get handles GET /admin/payouts/:ref
func (h *PayoutHandler) Get(c *gin.Context) {
	ref := c.Param("ref")
	payout, err := h.svc.GetByClientRef(c.Request.Context(), ref)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to retrieve payout",
		})
		return
	}
	if payout == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "no payout with that client ref",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"payout": toPayoutView(payout)})
}

// payoutView is a JSON-friendly projection of Payout. The Amount field is
// rendered as a decimal USDC string so clients don't have to know the
// 6-decimal scale.
type payoutView struct {
	ClientRef     string   `json:"clientRef"`
	ChainID       int64    `json:"chainId"`
	From          string   `json:"from"`
	To            string   `json:"to"`
	Amount        string   `json:"amount"`
	Nonce         uint64   `json:"nonce"`
	TxHash        string   `json:"txHash"`
	Status        TxStatus `json:"status"`
	SubmittedAt   string   `json:"submittedAt"`
	FinalizedAt   string   `json:"finalizedAt,omitempty"`
	BlockNumber   uint64   `json:"blockNumber,omitempty"`
	Confirmations uint64   `json:"confirmations,omitempty"`
	LastError     string   `json:"lastError,omitempty"`
}

func toPayoutView(p *Payout) payoutView {
	v := payoutView{
		ClientRef:   p.ClientRef,
		ChainID:     p.ChainID,
		From:        p.From,
		To:          p.To,
		Amount:      Format(p.Amount),
		Nonce:       p.Nonce,
		TxHash:      p.TxHash,
		Status:      p.Status,
		SubmittedAt: p.SubmittedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		LastError:   p.LastError,
	}
	if p.FinalizedAt != nil {
		v.FinalizedAt = p.FinalizedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if p.Receipt != nil {
		v.BlockNumber = p.Receipt.BlockNumber
		v.Confirmations = p.Receipt.Confirmations
	}
	return v
}
