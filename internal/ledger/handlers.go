package ledger

import (
	"context"
	"log/slog"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/idgen"
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
	logger   *slog.Logger
}

// NewHandler creates a new ledger handler
func NewHandler(ledger *Ledger, logger *slog.Logger) *Handler {
	return &Handler{ledger: ledger, logger: logger}
}

// NewHandlerWithWithdrawals creates a handler that can execute withdrawals
func NewHandlerWithWithdrawals(ledger *Ledger, executor WithdrawalExecutor, logger *slog.Logger) *Handler {
	return &Handler{ledger: ledger, executor: executor, logger: logger}
}

// RegisterRoutes sets up ledger routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/agents/:address/balance", h.GetBalance)
	r.GET("/agents/:address/ledger", h.GetHistory)
}

// RegisterAdminRoutes sets up admin-only ledger routes.
func (h *Handler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/admin/reconcile", h.Reconcile)
	r.GET("/admin/audit", h.QueryAudit)
	r.POST("/admin/reversals", h.Reverse)
}

// GetBalance handles GET /agents/:address/balance
func (h *Handler) GetBalance(c *gin.Context) {
	address := c.Param("address")

	// Support point-in-time query
	if tsStr := c.Query("at"); tsStr != "" {
		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_timestamp", "message": "Use RFC3339 format"})
			return
		}
		bal, err := h.ledger.BalanceAtTime(c.Request.Context(), address, ts)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "balance_error", "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"balance": bal, "at": tsStr})
		return
	}

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

		// Use a unique reference for the hold lifecycle so credit_draw_hold tracking works.
		// Include a random ID to prevent collision between concurrent same-amount withdrawals.
		holdRef := "withdrawal:" + address + ":" + idgen.WithPrefix("wd_")

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
			if relErr := h.ledger.ReleaseHold(c.Request.Context(), address, req.Amount, holdRef); relErr != nil {
				h.logger.Error("ReleaseHold failed after withdrawal transfer error: funds stuck in pending",
					"agent", address, "amount", req.Amount, "holdRef", holdRef, "error", relErr)
			}
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

// Reconcile handles GET /admin/reconcile — replays events vs actual balances.
func (h *Handler) Reconcile(c *gin.Context) {
	results, err := h.ledger.ReconcileAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "reconciliation_error",
			"message": err.Error(),
		})
		return
	}

	// Filter to show only discrepancies if requested
	if c.Query("discrepancies") == "true" {
		var filtered []*ReconciliationResult
		for _, r := range results {
			if !r.Match {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	c.JSON(http.StatusOK, gin.H{
		"results": results,
		"count":   len(results),
	})
}

// QueryAudit handles GET /admin/audit?agent=&from=&to=&operation=
func (h *Handler) QueryAudit(c *gin.Context) {
	if h.ledger.auditLogger == nil {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":   "not_configured",
			"message": "Audit logging is not enabled",
		})
		return
	}

	agentAddr := c.Query("agent")
	if agentAddr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "missing_agent",
			"message": "agent query parameter is required",
		})
		return
	}

	from := time.Time{}
	to := time.Now()
	if fromStr := c.Query("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			from = t
		}
	}
	if toStr := c.Query("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			to = t
		}
	}

	operation := c.Query("operation")
	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := h.ledger.auditLogger.QueryAudit(c.Request.Context(), strings.ToLower(agentAddr), from, to, operation, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "audit_error",
			"message": "Failed to query audit log",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"entries": entries,
		"count":   len(entries),
	})
}

// ReverseRequest for transaction reversal
type ReverseRequest struct {
	EntryID string `json:"entryId" binding:"required"`
	Reason  string `json:"reason" binding:"required"`
}

// Reverse handles POST /admin/reversals
func (h *Handler) Reverse(c *gin.Context) {
	var req ReverseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	adminID := c.GetString("agent_address") // from auth middleware
	if adminID == "" {
		adminID = "admin"
	}

	err := h.ledger.Reverse(c.Request.Context(), req.EntryID, req.Reason, adminID)
	if err != nil {
		status := http.StatusInternalServerError
		errCode := "reversal_error"

		switch err {
		case ErrEntryNotFound:
			status = http.StatusNotFound
			errCode = "entry_not_found"
		case ErrAlreadyReversed:
			status = http.StatusConflict
			errCode = "already_reversed"
		case ErrInsufficientBalance:
			status = http.StatusBadRequest
			errCode = "insufficient_balance"
		}

		c.JSON(status, gin.H{
			"error":   errCode,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":  "reversed",
		"message": "Entry reversed successfully",
		"entryId": req.EntryID,
	})
}
