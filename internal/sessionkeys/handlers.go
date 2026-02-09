package sessionkeys

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/mbd888/alancoin/internal/usdc"
	"github.com/mbd888/alancoin/internal/validation"
)

// TransferResult from a wallet transfer
type TransferResult struct {
	TxHash string
	From   string
	To     string
	Amount string
}

// WalletService executes on-chain transfers
type WalletService interface {
	Transfer(ctx context.Context, to common.Address, amount *big.Int) (*TransferResult, error)
}

// TransactionRecorder records transactions in the registry
type TransactionRecorder interface {
	RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID string) error
}

// BalanceService checks and debits agent balances
type BalanceService interface {
	CanSpend(ctx context.Context, agentAddr, amount string) (bool, error)
	Spend(ctx context.Context, agentAddr, amount, reference string) error
	Refund(ctx context.Context, agentAddr, amount, reference string) error
	Deposit(ctx context.Context, agentAddr, amount, reference string) error

	// Two-phase hold operations for safe transaction execution.
	// Hold moves funds from available → pending before on-chain transfer.
	// ConfirmHold moves from pending → total_out after confirmation.
	// ReleaseHold moves from pending → available if transfer fails.
	Hold(ctx context.Context, agentAddr, amount, reference string) error
	ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error
}

// EventEmitter broadcasts events to real-time subscribers
type EventEmitter interface {
	EmitTransaction(tx map[string]interface{})
	EmitSessionKeyUsed(keyID, agentAddr string, amount string)
}

// Handler provides HTTP handlers for session key operations
type Handler struct {
	manager  *Manager
	wallet   WalletService       // For executing transfers (optional)
	recorder TransactionRecorder // For recording txs (optional)
	balance  BalanceService      // For checking/debiting balances (optional)
	events   EventEmitter        // For broadcasting events (optional)
	logger   *slog.Logger
	demoMode bool // Skip on-chain transfers, use ledger only
}

// NewHandler creates a new session key handler
func NewHandler(manager *Manager, logger *slog.Logger) *Handler {
	return &Handler{manager: manager, logger: logger}
}

// NewHandlerWithExecution creates a handler that can execute real transfers
func NewHandlerWithExecution(manager *Manager, wallet WalletService, recorder TransactionRecorder, balance BalanceService, logger *slog.Logger) *Handler {
	return &Handler{
		manager:  manager,
		wallet:   wallet,
		recorder: recorder,
		balance:  balance,
		logger:   logger,
	}
}

// WithEvents adds an event emitter to the handler
func (h *Handler) WithEvents(events EventEmitter) *Handler {
	h.events = events
	return h
}

// WithDemoMode enables demo mode: balance holds work but on-chain transfers are skipped.
func (h *Handler) WithDemoMode() *Handler {
	h.demoMode = true
	return h
}

// RegisterRoutes sets up the session key routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Session key management
	r.POST("/agents/:address/sessions", h.CreateSessionKey)
	r.GET("/agents/:address/sessions", h.ListSessionKeys)
	r.GET("/agents/:address/sessions/:keyId", h.GetSessionKey)
	r.DELETE("/agents/:address/sessions/:keyId", h.RevokeSessionKey)

	// Transaction execution via session key
	r.POST("/agents/:address/sessions/:keyId/transact", h.Transact)
}

// CreateSessionKey handles POST /agents/:address/sessions
func (h *Handler) CreateSessionKey(c *gin.Context) {
	address := c.Param("address")

	var req SessionKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
		})
		return
	}

	key, err := h.manager.Create(c.Request.Context(), address, &req)
	if err != nil {
		// Return safe error message
		if ve, ok := err.(*ValidationError); ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   ve.Code,
				"message": ve.Message,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "creation_failed",
			"message": "Failed to create session key",
		})
		return
	}

	c.JSON(http.StatusCreated, key)
}

// ListSessionKeys handles GET /agents/:address/sessions
func (h *Handler) ListSessionKeys(c *gin.Context) {
	address := c.Param("address")

	keys, err := h.manager.List(c.Request.Context(), address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": "Failed to list session keys",
		})
		return
	}

	// Filter out sensitive data and add status
	type keyResponse struct {
		*SessionKey
		Status string `json:"status"`
	}

	response := make([]keyResponse, len(keys))
	for i, key := range keys {
		status := "active"
		if key.RevokedAt != nil {
			status = "revoked"
		} else if !key.IsActive() {
			status = "expired"
		}
		response[i] = keyResponse{
			SessionKey: key,
			Status:     status,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": response,
		"count":    len(response),
	})
}

// GetSessionKey handles GET /agents/:address/sessions/:keyId
func (h *Handler) GetSessionKey(c *gin.Context) {
	keyID := c.Param("keyId")

	key, err := h.manager.Get(c.Request.Context(), keyID)
	if err != nil {
		if err == ErrKeyNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Session key not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "get_failed",
			"message": "Failed to get session key",
		})
		return
	}

	// Add status
	status := "active"
	if key.RevokedAt != nil {
		status = "revoked"
	} else if !key.IsActive() {
		status = "expired"
	}

	c.JSON(http.StatusOK, gin.H{
		"session": key,
		"status":  status,
	})
}

// RevokeSessionKey handles DELETE /agents/:address/sessions/:keyId
func (h *Handler) RevokeSessionKey(c *gin.Context) {
	keyID := c.Param("keyId")

	err := h.manager.Revoke(c.Request.Context(), keyID)
	if err != nil {
		if err == ErrKeyNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "Session key not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "revoke_failed",
			"message": "Failed to revoke session key",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Session key revoked",
		"keyId":   keyID,
	})
}

// Transact handles POST /agents/:address/sessions/:keyId/transact
// This validates a cryptographically signed transaction and executes it
//
// The request must include:
// - to: recipient address
// - amount: USDC amount
// - nonce: unique number (must be > last used nonce)
// - timestamp: unix timestamp (must be within 5 minutes)
// - signature: ECDSA signature of "Alancoin|{to}|{amount}|{nonce}|{timestamp}"
//
// The signature must be created by the private key corresponding to the
// session key's publicKey.
func (h *Handler) Transact(c *gin.Context) {
	address := c.Param("address")
	keyID := c.Param("keyId")

	var req SignedTransactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
			"hint":    "Required: to, amount, nonce, timestamp, signature",
		})
		return
	}

	// Validate address and amount format before signature verification
	if errs := validation.Validate(
		validation.ValidAddress("to", req.To),
		validation.ValidAmount("amount", req.Amount),
	); len(errs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_failed",
			"message": errs.Error(),
		})
		return
	}

	// Get the session key
	key, err := h.manager.Get(c.Request.Context(), keyID)
	if err != nil {
		if err == ErrKeyNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "session_not_found",
				"message": "Session key not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	// Verify ownership
	if !strings.EqualFold(key.OwnerAddr, address) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "Session key does not belong to this agent",
		})
		return
	}

	// Acquire per-key lock(s) to prevent nonce TOCTOU replay attacks.
	// For delegated keys, lock the entire ancestor chain to prevent concurrent
	// sibling transactions from exceeding the parent budget.
	var unlockKey func()
	if key.ParentKeyID != "" {
		unlockKey = h.manager.LockKeyChain(c.Request.Context(), keyID)
	} else {
		unlockKey = h.manager.LockKey(keyID)
	}
	defer unlockKey()

	// Validate the signed transaction (signature + permissions)
	if err := h.manager.ValidateSigned(c.Request.Context(), keyID, &req); err != nil {
		if validationErr, ok := err.(*ValidationError); ok {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   validationErr.Code,
				"message": validationErr.Message,
			})
			return
		}
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "validation_failed",
			"message": "Transaction validation failed",
		})
		return
	}

	// Execute the transfer if wallet is configured
	var txHash string
	var executed bool

	if h.wallet != nil {
		// Parse amount to big.Int (USDC has 6 decimals)
		amountBig, ok := usdc.Parse(req.Amount)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_amount",
				"message": "Invalid amount format",
			})
			return
		}

		// Two-phase balance hold: hold funds before transfer, confirm after.
		// This prevents double-spend without risking permanent fund loss
		// if the on-chain transfer gets stuck in the mempool.
		if h.balance != nil {
			// Phase 1: Place hold (available → pending)
			if err := h.balance.Hold(c.Request.Context(), address, req.Amount, keyID); err != nil {
				c.JSON(http.StatusPaymentRequired, gin.H{
					"error":   "insufficient_balance",
					"message": "Agent has insufficient balance",
				})
				return
			}
		}

		if h.demoMode && h.balance != nil {
			// Demo mode: skip on-chain transfer, use synthetic tx hash.
			// Ledger hold was already placed above, just confirm it.
			txHash = fmt.Sprintf("0x%016x%016x", time.Now().UnixNano(), time.Now().UnixNano()^0xdeadbeef)
			executed = true
			if err := h.balance.ConfirmHold(c.Request.Context(), address, req.Amount, keyID); err != nil {
				h.logger.Error("demo ConfirmHold failed: funds stuck in pending",
					"agent", address, "amount", req.Amount, "keyId", keyID, "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "ledger_error",
					"message": "Failed to confirm balance hold",
				})
				return
			}

			// Credit the recipient in the ledger (deposit, not refund)
			if err := h.balance.Deposit(c.Request.Context(), req.To, req.Amount, txHash); err != nil {
				h.logger.Error("demo Deposit failed: sender debited but recipient not credited",
					"from", address, "to", req.To, "amount", req.Amount, "txHash", txHash, "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "ledger_error",
					"message": "Failed to credit recipient",
				})
				return
			}
		} else {
			// Production: execute on-chain transfer
			result, err := h.wallet.Transfer(c.Request.Context(), common.HexToAddress(req.To), amountBig)
			if err != nil {
				// Release the hold since transfer failed
				if h.balance != nil {
					if relErr := h.balance.ReleaseHold(c.Request.Context(), address, req.Amount, keyID); relErr != nil {
						h.logger.Warn("ReleaseHold failed after transfer error: funds may be stuck in pending",
							"agent", address, "amount", req.Amount, "keyId", keyID, "error", relErr)
					}
				}
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "transfer_failed",
					"message": "On-chain transfer failed",
				})
				return
			}

			txHash = result.TxHash
			executed = true

			// Phase 2: Confirm hold (pending → total_out)
			if h.balance != nil {
				if confirmErr := h.balance.ConfirmHold(c.Request.Context(), address, req.Amount, keyID); confirmErr != nil {
					h.logger.Error("ConfirmHold failed after on-chain transfer: funds in pending but transfer succeeded",
						"agent", address, "amount", req.Amount, "keyId", keyID, "txHash", result.TxHash, "error", confirmErr)
					c.JSON(http.StatusInternalServerError, gin.H{
						"status":  "partial_failure",
						"error":   "ledger_sync_failed",
						"message": "On-chain transfer succeeded but ledger update failed - contact support",
						"txHash":  result.TxHash,
						"amount":  req.Amount,
					})
					return
				}
			}
		}

		// Record in registry if available
		if h.recorder != nil {
			if recErr := h.recorder.RecordTransaction(c.Request.Context(), txHash, address, req.To, req.Amount, req.ServiceID); recErr != nil {
				h.logger.Warn("RecordTransaction failed: transaction executed but not recorded in registry",
					"txHash", txHash, "from", address, "to", req.To, "amount", req.Amount, "error", recErr)
			}
		}
	}

	// Record usage AFTER successful transfer (or in dry-run mode).
	// Recording before transfer would permanently consume budget on transfer failure.
	// For delegated keys, cascade the spend up to all ancestors.
	var usageErr error
	if key.ParentKeyID != "" {
		usageErr = h.manager.RecordUsageWithCascade(c.Request.Context(), keyID, req.Amount, req.Nonce)
	} else {
		usageErr = h.manager.RecordUsage(c.Request.Context(), keyID, req.Amount, req.Nonce)
	}
	if usageErr != nil {
		if !executed {
			// No transfer happened, safe to fail
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "usage_tracking_failed",
				"message": "Failed to record usage",
			})
			return
		}
		// Transfer already executed -- log the error but still return success
		// to avoid client retries that would double-spend
	}

	// Reload key to get updated usage
	key, err = h.manager.Get(c.Request.Context(), keyID)
	if err != nil || key == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "Failed to reload session key",
		})
		return
	}

	// Build response
	response := gin.H{
		"status":       "approved",
		"sessionKeyId": keyID,
		"from":         address,
		"to":           req.To,
		"amount":       req.Amount,
		"nonce":        req.Nonce,
		"serviceId":    req.ServiceID,
		"verification": gin.H{
			"signatureValid": true,
			"signerAddress":  key.PublicKey,
			"nonceValid":     true,
			"timestampValid": true,
		},
		"permissions": gin.H{
			"maxPerTransaction": key.Permission.MaxPerTransaction,
			"maxPerDay":         key.Permission.MaxPerDay,
			"maxTotal":          key.Permission.MaxTotal,
			"remainingDaily":    calculateRemaining(key.Permission.MaxPerDay, key.Usage.SpentToday),
			"remainingTotal":    calculateRemaining(key.Permission.MaxTotal, key.Usage.TotalSpent),
		},
		"usage": gin.H{
			"transactionCount": key.Usage.TransactionCount,
			"totalSpent":       key.Usage.TotalSpent,
			"spentToday":       key.Usage.SpentToday,
			"lastNonce":        key.Usage.LastNonce,
		},
	}

	// Add delegation info if this is a delegated key
	if key.ParentKeyID != "" {
		response["delegation"] = gin.H{
			"parentKeyId": key.ParentKeyID,
			"rootKeyId":   key.RootKeyID,
			"depth":       key.Depth,
			"label":       key.DelegationLabel,
		}
	}

	if executed {
		response["status"] = "executed"
		response["message"] = "Transaction executed on-chain"
		response["txHash"] = txHash

		// Emit real-time event
		if h.events != nil {
			eventData := map[string]interface{}{
				"txHash":      txHash,
				"from":        address,
				"to":          req.To,
				"amount":      req.Amount,
				"serviceType": req.ServiceID,
				"sessionKey":  keyID,
				"status":      "executed",
			}
			if key.ParentKeyID != "" {
				eventData["delegation"] = map[string]interface{}{
					"parentKeyId": key.ParentKeyID,
					"rootKeyId":   key.RootKeyID,
					"depth":       key.Depth,
				}
			}
			h.events.EmitTransaction(eventData)
			h.events.EmitSessionKeyUsed(keyID, address, req.Amount)
		}
	} else {
		response["message"] = "Transaction cryptographically verified (dry-run mode - no wallet configured)"
	}

	c.JSON(http.StatusOK, response)
}

// CreateDelegation handles POST /v1/sessions/:keyId/delegate
// Creates a child session key delegated from the parent key.
// Authentication is via ECDSA signature from the parent key (no API key needed).
func (h *Handler) CreateDelegation(c *gin.Context) {
	parentKeyID := c.Param("keyId")

	var req DelegateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
			"hint":    "Required: publicKey, maxTotal, nonce, timestamp, signature",
		})
		return
	}

	// Lock the parent key to prevent concurrent delegation/transactions
	unlockKey := h.manager.LockKey(parentKeyID)
	defer unlockKey()

	childKey, err := h.manager.CreateDelegated(c.Request.Context(), parentKeyID, &req)
	if err != nil {
		if ve, ok := err.(*ValidationError); ok {
			status := http.StatusBadRequest
			if ve.Code == "parent_not_active" || ve.Code == "ancestor_invalid" {
				status = http.StatusForbidden
			}
			c.JSON(status, gin.H{
				"error":   ve.Code,
				"message": ve.Message,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "delegation_failed",
			"message": "Failed to create delegated key",
		})
		return
	}

	// Emit real-time event
	if h.events != nil {
		h.events.EmitTransaction(map[string]interface{}{
			"event":       "delegation_created",
			"parentKeyId": parentKeyID,
			"childKeyId":  childKey.ID,
			"depth":       childKey.Depth,
			"maxTotal":    req.MaxTotal,
			"label":       req.DelegationLabel,
		})
	}

	c.JSON(http.StatusCreated, gin.H{
		"childKey": childKey,
		"delegation": gin.H{
			"parentKeyId": parentKeyID,
			"depth":       childKey.Depth,
			"rootKeyId":   childKey.RootKeyID,
			"label":       childKey.DelegationLabel,
		},
	})
}

// DelegationTreeNode represents a node in the delegation tree response
type DelegationTreeNode struct {
	KeyID            string                `json:"keyId"`
	PublicKey        string                `json:"publicKey"`
	Label            string                `json:"label,omitempty"`
	Depth            int                   `json:"depth"`
	MaxTotal         string                `json:"maxTotal,omitempty"`
	TotalSpent       string                `json:"totalSpent"`
	Remaining        string                `json:"remaining"`
	TransactionCount int                   `json:"transactionCount"`
	Active           bool                  `json:"active"`
	Children         []*DelegationTreeNode `json:"children,omitempty"`
}

// GetDelegationTree handles GET /v1/sessions/:keyId/tree
// Returns the full delegation tree rooted at the given key.
func (h *Handler) GetDelegationTree(c *gin.Context) {
	keyID := c.Param("keyId")

	key, err := h.manager.Get(c.Request.Context(), keyID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": "Session key not found",
		})
		return
	}

	tree := h.buildTreeNode(c.Request.Context(), key, MaxDelegationDepth+2)

	c.JSON(http.StatusOK, gin.H{
		"tree":      tree,
		"rootKeyId": key.RootKeyID,
		"ownerAddr": key.OwnerAddr,
	})
}

func (h *Handler) buildTreeNode(ctx context.Context, key *SessionKey, maxDepth int) *DelegationTreeNode {
	node := &DelegationTreeNode{
		KeyID:            key.ID,
		PublicKey:        key.PublicKey,
		Label:            key.DelegationLabel,
		Depth:            key.Depth,
		MaxTotal:         key.Permission.MaxTotal,
		TotalSpent:       key.Usage.TotalSpent,
		Remaining:        calculateRemaining(key.Permission.MaxTotal, key.Usage.TotalSpent),
		TransactionCount: key.Usage.TransactionCount,
		Active:           key.IsActive(),
	}

	// Depth guard prevents stack overflow from cyclic data
	if maxDepth <= 0 {
		return node
	}

	children, err := h.manager.store.GetByParent(ctx, key.ID)
	if err != nil || len(children) == 0 {
		return node
	}

	node.Children = make([]*DelegationTreeNode, 0, len(children))
	for _, child := range children {
		node.Children = append(node.Children, h.buildTreeNode(ctx, child, maxDepth-1))
	}

	return node
}

func calculateRemaining(limit string, spent string) string {
	if limit == "" {
		return "unlimited"
	}
	limitBig, ok := usdc.Parse(limit)
	if !ok {
		return "unknown"
	}
	spentBig, _ := usdc.Parse(spent)
	remaining := new(big.Int).Sub(limitBig, spentBig)
	if remaining.Sign() < 0 {
		return "0"
	}
	return usdc.Format(remaining)
}
