package withdrawals

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func safeMessage(status int, err error, fallback string) string {
	if status < 500 {
		return err.Error()
	}
	return fallback
}

// Handler exposes agent-initiated withdrawal over HTTP.
// Mount under a router group that has already applied authentication —
// the handler itself performs no auth.
type Handler struct {
	svc *Service
}

// NewHandler returns a handler bound to the given service.
// Pass nil to register 503 stubs that report withdrawals as disabled.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes wires agent-initiated withdrawal routes on the given group.
//
// The legacy ledger-only withdraw endpoint already owns
// POST /agents/:address/withdraw, so the two-phase on-chain version
// registers under /agents/:address/payouts to avoid collision.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h == nil || h.svc == nil {
		r.POST("/agents/:address/payouts", unavailable)
		return
	}
	r.POST("/agents/:address/payouts", h.Withdraw)
}

func unavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error":   "withdrawals_disabled",
		"message": "PAYOUTS_ENABLED is not set on this deployment",
	})
}

// withdrawReq is the POST body for Withdraw.
type withdrawReq struct {
	To        string `json:"to"        binding:"required"`
	Amount    string `json:"amount"    binding:"required"`
	ClientRef string `json:"clientRef" binding:"required"`
}

// Withdraw handles POST /agents/:address/withdraw
func (h *Handler) Withdraw(c *gin.Context) {
	agentAddr := c.Param("address")
	var req withdrawReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": err.Error(),
		})
		return
	}

	w, err := h.svc.Withdraw(c.Request.Context(), Request{
		AgentAddr: agentAddr,
		ToAddr:    req.To,
		Amount:    req.Amount,
		ClientRef: req.ClientRef,
	})
	if err != nil {
		// Pending: tx submitted, on-chain status unknown, hold retained.
		// Surface as 202 Accepted so clients know it is in-flight, not failed.
		if errors.Is(err, ErrPayoutPending) {
			body := gin.H{
				"error":   "payout_pending",
				"message": "Payout submitted; on-chain status unknown. Funds remain on hold pending reconciliation.",
			}
			if w != nil {
				body["withdrawal"] = w
			}
			c.JSON(http.StatusAccepted, body)
			return
		}

		status := http.StatusInternalServerError
		code := "internal_error"
		switch {
		case errors.Is(err, ErrMissingRef),
			errors.Is(err, ErrBadAgent),
			errors.Is(err, ErrBadRecipient),
			errors.Is(err, ErrBadAmount):
			status = http.StatusBadRequest
			code = "invalid_request"
		case errors.Is(err, ErrLedgerHold):
			status = http.StatusConflict
			code = "ledger_hold_failed"
		case errors.Is(err, ErrPayoutFailed):
			status = http.StatusBadGateway
			code = "payout_failed"
		}
		body := gin.H{
			"error":   code,
			"message": safeMessage(status, err, "Failed to process withdrawal"),
		}
		if w != nil {
			body["withdrawal"] = w
		}
		c.JSON(status, body)
		return
	}
	c.JSON(http.StatusOK, gin.H{"withdrawal": w})
}
