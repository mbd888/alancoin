package reconciliation

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock checkers ---

type mockLedgerChecker struct {
	mismatches int
	err        error
}

func (m *mockLedgerChecker) CheckAll(_ context.Context) (int, error) {
	return m.mismatches, m.err
}

type mockEscrowChecker struct {
	expired int
	err     error
}

func (m *mockEscrowChecker) CountExpired(_ context.Context) (int, error) {
	return m.expired, m.err
}

type mockStreamChecker struct {
	stale int
	err   error
}

func (m *mockStreamChecker) CountStale(_ context.Context) (int, error) {
	return m.stale, m.err
}

type mockHoldChecker struct {
	orphaned int
	err      error
}

func (m *mockHoldChecker) CountOrphaned(_ context.Context) (int, error) {
	return m.orphaned, m.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRunAll_AllHealthy(t *testing.T) {
	runner := NewRunner(testLogger()).
		WithLedger(&mockLedgerChecker{mismatches: 0}).
		WithEscrow(&mockEscrowChecker{expired: 0}).
		WithStream(&mockStreamChecker{stale: 0}).
		WithHold(&mockHoldChecker{orphaned: 0})

	report, err := runner.RunAll(context.Background())
	require.NoError(t, err)
	assert.True(t, report.Healthy)
	assert.Equal(t, 0, report.LedgerMismatches)
	assert.Equal(t, 0, report.StuckEscrows)
	assert.Equal(t, 0, report.StaleStreams)
	assert.Equal(t, 0, report.OrphanedHolds)
}

func TestRunAll_WithProblems(t *testing.T) {
	runner := NewRunner(testLogger()).
		WithLedger(&mockLedgerChecker{mismatches: 2}).
		WithEscrow(&mockEscrowChecker{expired: 1}).
		WithStream(&mockStreamChecker{stale: 3}).
		WithHold(&mockHoldChecker{orphaned: 0})

	report, err := runner.RunAll(context.Background())
	require.NoError(t, err)
	assert.False(t, report.Healthy)
	assert.Equal(t, 2, report.LedgerMismatches)
	assert.Equal(t, 1, report.StuckEscrows)
	assert.Equal(t, 3, report.StaleStreams)
	assert.Equal(t, 0, report.OrphanedHolds)
}

func TestRunAll_CheckerErrors(t *testing.T) {
	runner := NewRunner(testLogger()).
		WithLedger(&mockLedgerChecker{err: errors.New("db down")}).
		WithEscrow(&mockEscrowChecker{err: errors.New("timeout")})

	report, err := runner.RunAll(context.Background())
	require.NoError(t, err) // RunAll doesn't return checker errors
	// When checkers fail, their counts stay at 0 (zero value).
	assert.True(t, report.Healthy) // 0s everywhere = healthy
	assert.Equal(t, 0, report.LedgerMismatches)
}

func TestRunAll_NoCheckers(t *testing.T) {
	runner := NewRunner(testLogger())

	report, err := runner.RunAll(context.Background())
	require.NoError(t, err)
	assert.True(t, report.Healthy)
}

func TestLastReport_NilBeforeRun(t *testing.T) {
	runner := NewRunner(testLogger())
	assert.Nil(t, runner.LastReport())
}

func TestLastReport_CachedAfterRun(t *testing.T) {
	runner := NewRunner(testLogger()).
		WithLedger(&mockLedgerChecker{mismatches: 1})

	_, _ = runner.RunAll(context.Background())

	report := runner.LastReport()
	require.NotNil(t, report)
	assert.Equal(t, 1, report.LedgerMismatches)
	assert.False(t, report.Healthy)
}
