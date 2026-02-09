package ledger

import (
	"context"
	"math/big"
	"net/http"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/usdc"
	"github.com/mbd888/alancoin/internal/validation"
)

// validAmount checks that amount is a valid positive decimal number
var validAmount = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func isValidAmount(amount string) bool {
	return validAmount.MatchString(strings.TrimSpace(amount))
}

// WithdrawalExecutor executes on-chain withdrawals
type WithdrawalExecutor interface {
	Transfer(ctx context.Context, to common.Address, amount *big.Int) (txHash string, err error)
}

// Handler provides HTTP endpoints for ledger operations
type Handler struct {
	ledger   *Ledger
	executor WithdrawalExecutor // nil = withdrawals are pending only
}

// NewHandler creates a new ledger handler
func NewHandler(ledger *Ledger) *Handler {
	return &Handler{ledger: ledger}
}

// NewHandlerWithWithdrawals creates a handler that can execute withdrawals
func NewHandlerWithWithdrawals(ledger *Ledger, executor WithdrawalExecutor) *Handler {
	return &Handler{ledger: ledger, executor: executor}
}

// RegisterRoutes sets up ledger routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/agents/:address/balance", h.GetBalance)
	r.GET("/agents/:address/ledger", h.GetHistory)
}

// GetBalance handles GET /agents/:address/balance
func (h *Handler) GetBalance(c *gin.Context) {
	address := c.Param("address")

	balance, err := h.ledger.GetBalance(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "balance_error",
			"message": "Failed to retrieve balance",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"balance": balance,
	})
}

// GetHistory handles GET /agents/:address/ledger
func (h *Handler) GetHistory(c *gin.Context) {
	address := c.Param("address")
	limit := 50

	entries, err := h.ledger.GetHistory(c.Request.Context(), address, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "ledger_error",
			"message": "Failed to retrieve ledger history",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"entries": entries,
	})
}

// DepositRequest for manual deposit recording (admin use)
type DepositRequest struct {
	AgentAddress string `json:"agentAddress" binding:"required"`
	Amount       string `json:"amount" binding:"required"`
	TxHash       string `json:"txHash" binding:"required"`
}

// RecordDeposit handles POST /admin/deposits (for manual/webhook deposit recording)
func (h *Handler) RecordDeposit(c *gin.Context) {
	var req DepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	if !validation.IsValidEthAddress(req.AgentAddress) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_address",
			"message": "agentAddress must be a valid Ethereum address (0x + 40 hex chars)",
		})
		return
	}

	if !isValidAmount(req.Amount) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_amount",
			"message": "Amount must be a positive decimal number",
		})
		return
	}

	err := h.ledger.Deposit(c.Request.Context(), req.AgentAddress, req.Amount, req.TxHash)
	if err != nil {
		if err == ErrDuplicateDeposit {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "duplicate_deposit",
				"message": "Deposit already processed",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "deposit_error",
			"message": "Failed to record deposit",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":  "credited",
		"message": "Deposit credited to agent balance",
	})
}

// WithdrawRequest for withdrawal
type WithdrawRequest struct {
	Amount string `json:"amount" binding:"required"`
}

// RequestWithdrawal handles POST /agents/:address/withdraw
func (h *Handler) RequestWithdrawal(c *gin.Context) {
	address := c.Param("address")

	var req WithdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	if !isValidAmount(req.Amount) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_amount",
			"message": "Amount must be a positive decimal number",
		})
		return
	}

	// Execute withdrawal if executor is configured
	if h.executor != nil {
		// Parse amount
		amountBig, ok := usdc.Parse(req.Amount)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_amount",
				"message": "Invalid amount format",
			})
			return
		}

		// Use a unique reference for the hold lifecycle so credit_draw_hold tracking works
		holdRef := "withdrawal:" + address + ":" + req.Amount

		// Hold funds atomically before transfer to prevent TOCTOU double-spend
		if err := h.ledger.Hold(c.Request.Context(), address, req.Amount, holdRef); err != nil {
			if err == ErrInsufficientBalance {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "insufficient_balance",
					"message": "Insufficient balance for withdrawal",
				})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "balance_error",
					"message": "Failed to reserve funds",
				})
			}
			return
		}

		// Execute on-chain transfer
		txHash, err := h.executor.Transfer(c.Request.Context(), common.HexToAddress(address), amountBig)
		if err != nil {
			// Release the hold since transfer failed
			_ = h.ledger.ReleaseHold(c.Request.Context(), address, req.Amount, holdRef)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "transfer_failed",
				"message": "Failed to execute withdrawal",
			})
			return
		}

		// Confirm the hold (pending → total_out) — same reference as Hold
		if err := h.ledger.ConfirmHold(c.Request.Context(), address, req.Amount, holdRef); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status":  "partial_failure",
				"error":   "ledger_sync_failed",
				"message": "On-chain transfer succeeded but ledger update failed - contact support",
				"amount":  req.Amount,
				"txHash":  txHash,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "completed",
			"message": "Withdrawal executed",
			"amount":  req.Amount,
			"txHash":  txHash,
		})
		return
	}

	// No executor - check balance before accepting
	canSpend, err := h.ledger.CanSpend(c.Request.Context(), address, req.Amount)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "balance_error",
			"message": "Failed to check balance",
		})
		return
	}
	if !canSpend {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "insufficient_balance",
			"message": "Insufficient balance for withdrawal",
		})
		return
	}

	// No executor - return pending status
	c.JSON(http.StatusAccepted, gin.H{
		"status":  "pending",
		"message": "Withdrawal request received",
		"amount":  req.Amount,
		"note":    "Withdrawals are processed within 24 hours",
	})
}
