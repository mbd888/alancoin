package escrow

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// MultiStepMemoryStore is an in-memory multistep escrow store for demo/development.
type MultiStepMemoryStore struct {
	escrows map[string]*MultiStepEscrow
	steps   map[string]map[int]Step // escrow_id -> step_index -> Step
	mu      sync.RWMutex
}

// NewMultiStepMemoryStore creates a new in-memory multistep escrow store.
func NewMultiStepMemoryStore() *MultiStepMemoryStore {
	return &MultiStepMemoryStore{
		escrows: make(map[string]*MultiStepEscrow),
		steps:   make(map[string]map[int]Step),
	}
}

func (m *MultiStepMemoryStore) Create(ctx context.Context, mse *MultiStepEscrow) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := *mse
	if mse.PlannedSteps != nil {
		cp.PlannedSteps = make([]PlannedStep, len(mse.PlannedSteps))
		copy(cp.PlannedSteps, mse.PlannedSteps)
	}
	m.escrows[mse.ID] = &cp
	m.steps[mse.ID] = make(map[int]Step)
	return nil
}

func (m *MultiStepMemoryStore) Get(ctx context.Context, id string) (*MultiStepEscrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mse, ok := m.escrows[id]
	if !ok {
		return nil, ErrMultiStepNotFound
	}
	cp := *mse
	if mse.PlannedSteps != nil {
		cp.PlannedSteps = make([]PlannedStep, len(mse.PlannedSteps))
		copy(cp.PlannedSteps, mse.PlannedSteps)
	}
	return &cp, nil
}

func (m *MultiStepMemoryStore) RecordStep(ctx context.Context, id string, step Step) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mse, ok := m.escrows[id]
	if !ok {
		return ErrMultiStepNotFound
	}

	stepMap, ok := m.steps[id]
	if !ok {
		return ErrMultiStepNotFound
	}

	if _, exists := stepMap[step.StepIndex]; exists {
		return ErrDuplicateStep
	}

	stepMap[step.StepIndex] = step

	// Update counters
	spentBig, _ := usdc.Parse(mse.SpentAmount)
	amountBig, _ := usdc.Parse(step.Amount)
	newSpent := new(big.Int).Add(spentBig, amountBig)

	mse.SpentAmount = usdc.Format(newSpent)
	mse.ConfirmedSteps++
	mse.UpdatedAt = time.Now()

	return nil
}

// DeleteStep reverses a RecordStep: removes the step and decrements counters.
func (m *MultiStepMemoryStore) DeleteStep(ctx context.Context, id string, stepIndex int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mse, ok := m.escrows[id]
	if !ok {
		return ErrMultiStepNotFound
	}

	stepMap, ok := m.steps[id]
	if !ok {
		return ErrMultiStepNotFound
	}

	step, exists := stepMap[stepIndex]
	if !exists {
		return ErrStepOutOfRange
	}

	amountBig, _ := usdc.Parse(step.Amount)
	spentBig, _ := usdc.Parse(mse.SpentAmount)
	newSpent := new(big.Int).Sub(spentBig, amountBig)

	mse.SpentAmount = usdc.Format(newSpent)
	mse.ConfirmedSteps--
	mse.UpdatedAt = time.Now()
	delete(stepMap, stepIndex)

	return nil
}

func (m *MultiStepMemoryStore) Abort(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mse, ok := m.escrows[id]
	if !ok {
		return ErrMultiStepNotFound
	}
	mse.Status = MSAborted
	mse.UpdatedAt = time.Now()
	return nil
}

func (m *MultiStepMemoryStore) Complete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mse, ok := m.escrows[id]
	if !ok {
		return ErrMultiStepNotFound
	}
	mse.Status = MSCompleted
	mse.UpdatedAt = time.Now()
	return nil
}

// Compile-time assertion.
var _ MultiStepStore = (*MultiStepMemoryStore)(nil)
