package withdrawals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Service runs the withdrawal two-phase flow.
// Construct once at startup and share across goroutines.
type Service struct {
	ledger  Ledger
	payouts Payouts
	logger  *slog.Logger
}

// NewService returns a ready-to-use Service. Both ledger and payouts are required.
func NewService(ledger Ledger, payouts Payouts, logger *slog.Logger) (*Service, error) {
	if ledger == nil || payouts == nil {
		return nil, errors.New("withdrawals: ledger and payouts are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{ledger: ledger, payouts: payouts, logger: logger}, nil
}

// Withdraw reserves funds via the ledger, submits an on-chain payout, and
// either finalizes the debit (success) or releases it (failure/drop).
//
// Blocks until the payout reaches a final on-chain state. Callers running
// this from an HTTP handler should budget for L2-typical finality latency.
func (s *Service) Withdraw(ctx context.Context, req Request) (*Withdrawal, error) {
	if req.ClientRef == "" {
		return nil, ErrMissingRef
	}
	if !isValidAddr(req.AgentAddr) {
		return nil, ErrBadAgent
	}
	if !isValidAddr(req.ToAddr) {
		return nil, ErrBadRecipient
	}
	if !isPositiveDecimal(req.Amount) {
		return nil, ErrBadAmount
	}

	agent := strings.ToLower(req.AgentAddr)
	to := strings.ToLower(req.ToAddr)
	ref := holdReference(req.ClientRef)

	if err := s.ledger.Hold(ctx, agent, req.Amount, ref); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLedgerHold, err)
	}

	result, payoutErr := s.payouts.Send(ctx, to, req.Amount, req.ClientRef)
	if payoutErr != nil {
		// Submission or finalization errored before we have a definitive
		// status. Treat as "unknown" and release the hold so the agent's
		// balance is restored. The operator can re-issue with the same
		// ClientRef after investigation.
		if relErr := s.ledger.ReleaseHold(ctx, agent, req.Amount, ref); relErr != nil {
			s.logger.Error("withdrawals: release hold after payout error failed",
				"agent", agent, "ref", ref, "payout_err", payoutErr, "release_err", relErr)
			// Surface both failures. The hold is now stuck — humans must resolve.
			return nil, fmt.Errorf("%w: %v; release also failed: %v", ErrPayoutFailed, payoutErr, relErr)
		}
		return &Withdrawal{
			ClientRef: req.ClientRef,
			AgentAddr: agent,
			ToAddr:    to,
			Amount:    req.Amount,
			Status:    "failed",
			Error:     payoutErr.Error(),
		}, fmt.Errorf("%w: %v", ErrPayoutFailed, payoutErr)
	}

	switch result.Status {
	case "success":
		if err := s.ledger.ConfirmHold(ctx, agent, req.Amount, ref); err != nil {
			// The money has already moved on-chain. We MUST NOT release the
			// hold because that would double-credit the agent. Log loudly
			// and surface so ops can reconcile manually.
			s.logger.Error("withdrawals: on-chain success but ConfirmHold failed — manual reconcile required",
				"agent", agent, "ref", ref, "tx", result.TxHash, "err", err)
			return &Withdrawal{
				ClientRef:   req.ClientRef,
				AgentAddr:   agent,
				ToAddr:      to,
				Amount:      req.Amount,
				Status:      "success",
				TxHash:      result.TxHash,
				SubmittedAt: result.SubmittedAt,
				FinalizedAt: result.FinalizedAt,
				Error:       "ledger reconciliation failed: " + err.Error(),
			}, fmt.Errorf("withdrawals: on-chain success but ledger reconciliation failed: %w", err)
		}
	case "failed", "dropped":
		if err := s.ledger.ReleaseHold(ctx, agent, req.Amount, ref); err != nil {
			s.logger.Error("withdrawals: release after on-chain failure errored",
				"agent", agent, "ref", ref, "status", result.Status, "err", err)
			return nil, fmt.Errorf("withdrawals: release hold: %w", err)
		}
	default: // "pending" or unknown
		// Payout service returned a final-state contract; anything else is
		// a bug. Don't touch the hold — leave it for humans.
		s.logger.Error("withdrawals: unexpected payout status; hold left in place for manual review",
			"agent", agent, "ref", ref, "status", result.Status)
		return nil, fmt.Errorf("withdrawals: unexpected payout status %q", result.Status)
	}

	return &Withdrawal{
		ClientRef:   req.ClientRef,
		AgentAddr:   agent,
		ToAddr:      to,
		Amount:      req.Amount,
		Status:      result.Status,
		TxHash:      result.TxHash,
		SubmittedAt: result.SubmittedAt,
		FinalizedAt: result.FinalizedAt,
		Error:       result.Error,
	}, nil
}

// --- small validation helpers (kept here rather than in withdrawals.go
// so the data types file stays declaration-only) ---

func isValidAddr(s string) bool {
	if len(s) != 42 || (s[0] != '0') || (s[1] != 'x' && s[1] != 'X') {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// isPositiveDecimal accepts strings like "1", "1.5", "0.000001".
// Rejects empty, negative, and non-numeric inputs.
func isPositiveDecimal(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	seenDigit := false
	seenDot := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			if r > '0' {
				seenDigit = true
			}
		case r == '.':
			if seenDot {
				return false
			}
			seenDot = true
		default:
			return false
		}
	}
	// Require at least one non-zero digit so "0" and "0.000" are rejected.
	return seenDigit
}
