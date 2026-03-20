package ledger

import (
	"context"
	"errors"
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
	"github.com/mbd888/alancoin/internal/pagination"
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

// ReputationScorer provides reputation scores for credit decisions.
type ReputationScorer interface {
	GetScore(ctx context.Context, address string) (float64, string, error)
}

// Handler provides HTTP endpoints for ledger operations
type Handler struct {
	ledger     *Ledger
	executor   WithdrawalExecutor // nil = withdrawals are pending only
	reputation ReputationScorer   // nil = credit applications denied
	logger     *slog.Logger
}

// NewHandler creates a new ledger handler
func NewHandler(ledger *Ledger, logger *slog.Logger) *Handler {
	return &Handler{ledger: ledger, logger: logger}
}

// NewHandlerWithWithdrawals creates a handler that can execute withdrawals
func NewHandlerWithWithdrawals(ledger *Ledger, executor WithdrawalExecutor, logger *slog.Logger) *Handler {
	return &Handler{ledger: ledger, executor: executor, logger: logger}
}

// WithReputation sets the reputation scorer for credit decisions.
func (h *Handler) WithReputation(r ReputationScorer) *Handler {
	h.reputation = r
	return h
}

// RegisterRoutes sets up ledger routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/agents/:address/balance", h.GetBalance)
	r.GET("/agents/:address/ledger", h.GetHistory)
}

// RegisterCreditRoutes sets up credit-related routes.
func (h *Handler) RegisterCreditRoutes(r *gin.RouterGroup) {
	r.GET("/agents/:address/credit", h.GetCreditInfo)
	r.POST("/agents/:address/credit/apply", h.ApplyForCredit)
	r.GET("/credit/active", h.ListActiveCredit)
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
			h.logger.Error("balance lookup failed", "agent", address, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "balance_error", "message": "Failed to retrieve balance"})
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
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	cursor, err := pagination.Decode(c.Query("cursor"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_cursor",
			"message": "Invalid pagination cursor",
		})
		return
	}

	var entries []*Entry
	if cursor != nil {
		entries, err = h.ledger.GetHistoryPage(c.Request.Context(), address, limit+1, cursor.CreatedAt, cursor.ID)
	} else {
		entries, err = h.ledger.GetHistoryPage(c.Request.Context(), address, limit+1, time.Time{}, "")
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "ledger_error",
			"message": "Failed to retrieve ledger history",
		})
		return
	}

	entries, nextCursor, hasMore := pagination.ComputePage(entries, limit, func(e *Entry) (time.Time, string) {
		return e.CreatedAt, e.ID
	})

	resp := gin.H{"entries": entries}
	if hasMore {
		resp["nextCursor"] = nextCursor
		resp["hasMore"] = true
	}
	c.JSON(http.StatusOK, resp)
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

	// No executor — hold funds atomically to prevent TOCTOU double-spend.
	// The hold will be released or confirmed when the withdrawal is processed out-of-band.
	holdRef := "withdrawal:" + address + ":" + idgen.WithPrefix("wd_")
	if err := h.ledger.Hold(c.Request.Context(), address, req.Amount, holdRef); err != nil {
		if err == ErrInsufficientBalance {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "insufficient_balance",
				"message": "Insufficient balance for withdrawal",
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "balance_error",
				"message": "Failed to reserve funds for withdrawal",
			})
		}
		return
	}

	// Funds are now locked — return pending status
	c.JSON(http.StatusAccepted, gin.H{
		"status":  "pending",
		"message": "Withdrawal request received — funds held pending processing",
		"amount":  req.Amount,
		"holdRef": holdRef,
		"note":    "Withdrawals are processed within 24 hours",
	})
}

// Reconcile handles GET /admin/reconcile — replays events vs actual balances.
func (h *Handler) Reconcile(c *gin.Context) {
	results, err := h.ledger.ReconcileAll(c.Request.Context())
	if err != nil {
		h.logger.Error("reconciliation failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "reconciliation_error",
			"message": "Reconciliation check failed",
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

	adminID := c.GetString("authAgentAddr") // from auth middleware
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

		msg := "Reversal failed"
		if errors.Is(err, ErrEntryNotFound) {
			msg = "Entry not found"
		} else if errors.Is(err, ErrAlreadyReversed) {
			msg = "Entry already reversed"
		} else if errors.Is(err, ErrInsufficientBalance) {
			msg = "Insufficient balance to reverse"
		} else {
			h.logger.Error("ledger reversal failed", "entryId", req.EntryID, "error", err)
		}
		c.JSON(status, gin.H{
			"error":   errCode,
			"message": msg,
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":  "reversed",
		"message": "Entry reversed successfully",
		"entryId": req.EntryID,
	})
}

// GetCreditInfo handles GET /agents/:address/credit
func (h *Handler) GetCreditInfo(c *gin.Context) {
	address := c.Param("address")

	creditLimit, creditUsed, err := h.ledger.StoreRef().GetCreditInfo(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "credit_error",
			"message": "Failed to retrieve credit info",
		})
		return
	}

	// Compute available credit
	limitBig, _ := usdc.Parse(creditLimit)
	usedBig, _ := usdc.Parse(creditUsed)
	availableBig := new(big.Int).Sub(limitBig, usedBig)
	if availableBig.Sign() < 0 {
		availableBig.SetInt64(0)
	}

	c.JSON(http.StatusOK, gin.H{
		"address":   address,
		"limit":     creditLimit,
		"used":      creditUsed,
		"available": usdc.Format(availableBig),
	})
}

// ApplyForCredit handles POST /agents/:address/credit/apply
// Simple rule: if reputation score > 50, auto-approve with limit proportional to score.
func (h *Handler) ApplyForCredit(c *gin.Context) {
	address := c.Param("address")

	if h.reputation == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "not_configured",
			"message": "Credit scoring is not available",
		})
		return
	}

	score, tier, err := h.reputation.GetScore(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "reputation_error",
			"message": "Failed to retrieve reputation score",
		})
		return
	}

	// Validate score is in expected range to prevent calculation errors
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	const minScore = 50.0
	if score < minScore {
		c.JSON(http.StatusOK, gin.H{
			"status":  "denied",
			"score":   score,
			"tier":    tier,
			"message": "Reputation score below minimum threshold (50)",
		})
		return
	}

	// Credit limit proportional to score: score 50 -> 10 USDC, score 100 -> 100 USDC
	// Linear scale: limit = (score - 50) * 1.8 + 10
	limitFloat := (score-50)*1.8 + 10
	// Convert to USDC micro-units (6 decimals) then format
	limitMicro := int64(limitFloat * 1_000_000)
	limitBig := big.NewInt(limitMicro)
	limitStr := usdc.Format(limitBig)

	if err := h.ledger.StoreRef().SetCreditLimit(c.Request.Context(), address, limitStr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "credit_error",
			"message": "Failed to set credit limit",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "approved",
		"score":  score,
		"tier":   tier,
		"limit":  limitStr,
	})
}

// ListActiveCredit handles GET /credit/active
func (h *Handler) ListActiveCredit(c *gin.Context) {
	ctx := c.Request.Context()
	store := h.ledger.StoreRef()

	// Use the ActiveCreditLister interface if the store supports it,
	// otherwise return an empty list.
	type activeCreditLister interface {
		ListActiveCredits(ctx context.Context) ([]ActiveCredit, error)
	}

	lister, ok := store.(activeCreditLister)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"credits": []interface{}{}, "count": 0})
		return
	}

	credits, err := lister.ListActiveCredits(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "credit_error",
			"message": "Failed to list active credits",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"credits": credits,
		"count":   len(credits),
	})
}
