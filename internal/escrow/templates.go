package escrow

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Template errors
var (
	ErrTemplateNotFound      = errors.New("template not found")
	ErrMilestoneNotFound     = errors.New("milestone not found")
	ErrMilestonesInvalid     = errors.New("milestones invalid")
	ErrMilestoneAlreadyDone  = errors.New("milestone already released")
	ErrAllMilestonesReleased = errors.New("all milestones already released")
)

// Milestone defines a single milestone within a template.
type Milestone struct {
	Name        string `json:"name"`
	Percentage  int    `json:"percentage"` // 1-100
	Description string `json:"description,omitempty"`
	Criteria    string `json:"criteria,omitempty"`
}

// EscrowTemplate is a reusable template for milestone-based escrows.
type EscrowTemplate struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	CreatorAddr      string      `json:"creatorAddr"`
	Milestones       []Milestone `json:"milestones"`
	TotalAmount      string      `json:"totalAmount"`
	AutoReleaseHours int         `json:"autoReleaseHours"`
	CreatedAt        time.Time   `json:"createdAt"`
	UpdatedAt        time.Time   `json:"updatedAt"`
}

// EscrowMilestone is a concrete milestone instance tied to an escrow.
type EscrowMilestone struct {
	ID             int        `json:"id"`
	EscrowID       string     `json:"escrowId"`
	TemplateID     string     `json:"templateId"`
	MilestoneIndex int        `json:"milestoneIndex"`
	Name           string     `json:"name"`
	Percentage     int        `json:"percentage"`
	Description    string     `json:"description,omitempty"`
	Criteria       string     `json:"criteria,omitempty"`
	Released       bool       `json:"released"`
	ReleasedAt     *time.Time `json:"releasedAt,omitempty"`
	ReleasedAmount string     `json:"releasedAmount,omitempty"`
}

// TemplateStore persists escrow templates and milestones.
type TemplateStore interface {
	CreateTemplate(ctx context.Context, t *EscrowTemplate) error
	GetTemplate(ctx context.Context, id string) (*EscrowTemplate, error)
	ListTemplates(ctx context.Context, limit int) ([]*EscrowTemplate, error)
	ListTemplatesByCreator(ctx context.Context, creatorAddr string, limit int) ([]*EscrowTemplate, error)

	CreateMilestone(ctx context.Context, m *EscrowMilestone) error
	GetMilestone(ctx context.Context, escrowID string, index int) (*EscrowMilestone, error)
	UpdateMilestone(ctx context.Context, m *EscrowMilestone) error
	ListMilestones(ctx context.Context, escrowID string) ([]*EscrowMilestone, error)
}

// TemplateService implements template and milestone business logic.
type TemplateService struct {
	templateStore TemplateStore
	escrowStore   Store
	ledger        LedgerService
	locks         sync.Map
}

// NewTemplateService creates a new template service.
func NewTemplateService(ts TemplateStore, es Store, ledger LedgerService) *TemplateService {
	return &TemplateService{
		templateStore: ts,
		escrowStore:   es,
		ledger:        ledger,
	}
}

func (s *TemplateService) escrowLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// CreateTemplate creates a reusable escrow template.
func (s *TemplateService) CreateTemplate(ctx context.Context, req CreateTemplateRequest) (*EscrowTemplate, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrMilestonesInvalid)
	}
	if len(req.Name) > 255 {
		return nil, fmt.Errorf("%w: name too long", ErrMilestonesInvalid)
	}

	amountBig, ok := usdc.Parse(req.TotalAmount)
	if !ok || amountBig.Sign() <= 0 {
		return nil, fmt.Errorf("%w: totalAmount must be positive", ErrInvalidAmount)
	}

	if len(req.Milestones) == 0 || len(req.Milestones) > 20 {
		return nil, fmt.Errorf("%w: must have 1-20 milestones", ErrMilestonesInvalid)
	}

	totalPct := 0
	for i, m := range req.Milestones {
		if strings.TrimSpace(m.Name) == "" {
			return nil, fmt.Errorf("%w: milestone %d name is required", ErrMilestonesInvalid, i)
		}
		if len(m.Name) > 255 {
			return nil, fmt.Errorf("%w: milestone %d name too long", ErrMilestonesInvalid, i)
		}
		if m.Percentage <= 0 || m.Percentage > 100 {
			return nil, fmt.Errorf("%w: milestone %d percentage must be 1-100", ErrMilestonesInvalid, i)
		}
		totalPct += m.Percentage
	}
	if totalPct != 100 {
		return nil, fmt.Errorf("%w: milestone percentages must sum to 100, got %d", ErrMilestonesInvalid, totalPct)
	}

	autoReleaseHours := req.AutoReleaseHours
	if autoReleaseHours <= 0 {
		autoReleaseHours = 24
	}

	now := time.Now()
	tmpl := &EscrowTemplate{
		ID:               generateTemplateID(),
		Name:             strings.TrimSpace(req.Name),
		CreatorAddr:      strings.ToLower(req.CreatorAddr),
		Milestones:       req.Milestones,
		TotalAmount:      usdc.Format(amountBig),
		AutoReleaseHours: autoReleaseHours,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.templateStore.CreateTemplate(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("failed to create template: %w", err)
	}
	return tmpl, nil
}

// GetTemplate returns a template by ID.
func (s *TemplateService) GetTemplate(ctx context.Context, id string) (*EscrowTemplate, error) {
	return s.templateStore.GetTemplate(ctx, id)
}

// ListTemplates returns templates.
func (s *TemplateService) ListTemplates(ctx context.Context, limit int) ([]*EscrowTemplate, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.templateStore.ListTemplates(ctx, limit)
}

// InstantiateTemplate creates an escrow from a template with milestones.
func (s *TemplateService) InstantiateTemplate(ctx context.Context, templateID string, req InstantiateRequest) (*Escrow, []*EscrowMilestone, error) {
	tmpl, err := s.templateStore.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, nil, err
	}

	buyerAddr := strings.ToLower(req.BuyerAddr)
	sellerAddr := strings.ToLower(req.SellerAddr)
	if buyerAddr == sellerAddr {
		return nil, nil, fmt.Errorf("%w: buyer and seller must be different", ErrInvalidAmount)
	}

	amountBig, _ := usdc.Parse(tmpl.TotalAmount)
	amountStr := usdc.Format(amountBig)

	// Lock buyer funds
	ref := fmt.Sprintf("template_escrow:%s:%s", tmpl.ID, buyerAddr)
	if err := s.ledger.EscrowLock(ctx, buyerAddr, amountStr, ref); err != nil {
		return nil, nil, fmt.Errorf("failed to lock buyer funds: %w", err)
	}

	autoRelease := time.Duration(tmpl.AutoReleaseHours) * time.Hour
	now := time.Now()

	escrow := &Escrow{
		ID:            generateEscrowID(),
		BuyerAddr:     buyerAddr,
		SellerAddr:    sellerAddr,
		Amount:        amountStr,
		Status:        StatusPending,
		AutoReleaseAt: now.Add(autoRelease),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.escrowStore.Create(ctx, escrow); err != nil {
		return nil, nil, fmt.Errorf("failed to create escrow: %w", err)
	}

	// Create milestone instances. If any milestone creation fails,
	// refund the escrowed funds to prevent permanent fund lockup.
	milestones := make([]*EscrowMilestone, 0, len(tmpl.Milestones))
	for i, m := range tmpl.Milestones {
		em := &EscrowMilestone{
			EscrowID:       escrow.ID,
			TemplateID:     tmpl.ID,
			MilestoneIndex: i,
			Name:           m.Name,
			Percentage:     m.Percentage,
			Description:    m.Description,
			Criteria:       m.Criteria,
			Released:       false,
		}
		if err := s.templateStore.CreateMilestone(ctx, em); err != nil {
			// Compensate: refund buyer's escrowed funds
			if refundErr := s.ledger.RefundEscrow(ctx, buyerAddr, amountStr, ref); refundErr != nil {
				log.Printf("CRITICAL: template %s milestone %d failed AND refund failed: %v", tmpl.ID, i, refundErr)
			}
			return nil, nil, fmt.Errorf("failed to create milestone %d: %w", i, err)
		}
		milestones = append(milestones, em)
	}

	return escrow, milestones, nil
}

// ReleaseMilestone releases funds for a specific milestone.
// Only the buyer can release milestones. The released amount is computed
// server-side from escrow.Amount * milestone.Percentage / 100.
func (s *TemplateService) ReleaseMilestone(ctx context.Context, escrowID string, milestoneIndex int, callerAddr string) (*EscrowMilestone, error) {
	mu := s.escrowLock(escrowID)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.escrowStore.Get(ctx, escrowID)
	if err != nil {
		return nil, err
	}

	// Only buyer can release milestones
	if strings.ToLower(callerAddr) != escrow.BuyerAddr {
		return nil, ErrUnauthorized
	}

	// Must be in pending or delivered state
	if escrow.Status != StatusPending && escrow.Status != StatusDelivered {
		return nil, ErrInvalidStatus
	}

	milestone, err := s.templateStore.GetMilestone(ctx, escrowID, milestoneIndex)
	if err != nil {
		return nil, err
	}

	if milestone.Released {
		return nil, ErrMilestoneAlreadyDone
	}

	// Compute release amount: escrow.Amount * percentage / 100
	amountBig, _ := usdc.Parse(escrow.Amount)
	releaseAmount := new(big.Int).Mul(amountBig, big.NewInt(int64(milestone.Percentage)))
	releaseAmount.Div(releaseAmount, big.NewInt(100))
	releaseStr := usdc.Format(releaseAmount)

	// Release escrow funds for this milestone
	ref := fmt.Sprintf("milestone:%s:%d", escrowID, milestoneIndex)
	if err := s.ledger.ReleaseEscrow(ctx, escrow.BuyerAddr, escrow.SellerAddr, releaseStr, ref); err != nil {
		return nil, fmt.Errorf("failed to release milestone funds: %w", err)
	}

	// Mark milestone released
	now := time.Now()
	milestone.Released = true
	milestone.ReleasedAt = &now
	milestone.ReleasedAmount = releaseStr

	if err := s.templateStore.UpdateMilestone(ctx, milestone); err != nil {
		return nil, fmt.Errorf("failed to update milestone: %w", err)
	}

	// Check if all milestones released → mark escrow as released
	allMilestones, err := s.templateStore.ListMilestones(ctx, escrowID)
	if err == nil {
		allReleased := true
		for _, m := range allMilestones {
			if !m.Released {
				allReleased = false
				break
			}
		}
		if allReleased {
			escrow.Status = StatusReleased
			resolvedAt := now
			escrow.ResolvedAt = &resolvedAt
			escrow.UpdatedAt = now
			if err := s.escrowStore.Update(ctx, escrow); err != nil {
				log.Printf("CRITICAL: escrow %s all milestones released but status update failed: %v", escrowID, err)
			}
		}
	}

	return milestone, nil
}

// ListMilestones returns all milestones for an escrow.
func (s *TemplateService) ListMilestones(ctx context.Context, escrowID string) ([]*EscrowMilestone, error) {
	return s.templateStore.ListMilestones(ctx, escrowID)
}

// Request types

// CreateTemplateRequest is the request body for POST /escrow/templates.
type CreateTemplateRequest struct {
	Name             string      `json:"name" binding:"required"`
	CreatorAddr      string      `json:"creatorAddr" binding:"required"`
	Milestones       []Milestone `json:"milestones" binding:"required"`
	TotalAmount      string      `json:"totalAmount" binding:"required"`
	AutoReleaseHours int         `json:"autoReleaseHours"`
}

// InstantiateRequest is the request body for POST /escrow/templates/:id/instantiate.
type InstantiateRequest struct {
	BuyerAddr  string `json:"buyerAddr" binding:"required"`
	SellerAddr string `json:"sellerAddr" binding:"required"`
}

// ReleaseMilestoneRequest is used internally (caller addr from auth context).
// No request body needed — milestone index from URL path.

func generateTemplateID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("tmpl_%x", b)
}
