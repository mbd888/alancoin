package escrow

import (
	"errors"
	"testing"
)

func TestValidTransitions(t *testing.T) {
	valid := []struct {
		from, to Status
	}{
		{StatusPending, StatusDelivered},
		{StatusPending, StatusReleased},
		{StatusPending, StatusDisputed},
		{StatusPending, StatusExpired},
		{StatusDelivered, StatusReleased},
		{StatusDelivered, StatusDisputed},
		{StatusDelivered, StatusExpired},
		{StatusDisputed, StatusArbitrating},
		{StatusDisputed, StatusReleased},
		{StatusDisputed, StatusRefunded},
		{StatusArbitrating, StatusReleased},
		{StatusArbitrating, StatusRefunded},
	}

	for _, tc := range valid {
		if !ValidTransition(tc.from, tc.to) {
			t.Errorf("expected %s → %s to be valid", tc.from, tc.to)
		}
	}
}

func TestInvalidTransitions(t *testing.T) {
	invalid := []struct {
		from, to Status
	}{
		// Terminal states have no outgoing transitions
		{StatusReleased, StatusPending},
		{StatusReleased, StatusDisputed},
		{StatusRefunded, StatusPending},
		{StatusRefunded, StatusReleased},
		{StatusExpired, StatusPending},
		{StatusExpired, StatusReleased},

		// Can't go backwards
		{StatusDelivered, StatusPending},
		{StatusDisputed, StatusPending},
		{StatusDisputed, StatusDelivered},
		{StatusArbitrating, StatusPending},
		{StatusArbitrating, StatusDelivered},
		{StatusArbitrating, StatusDisputed},

		// Can't skip states
		{StatusPending, StatusArbitrating},
		{StatusPending, StatusRefunded},
		{StatusDelivered, StatusArbitrating},
		{StatusDelivered, StatusRefunded},
	}

	for _, tc := range invalid {
		if ValidTransition(tc.from, tc.to) {
			t.Errorf("expected %s → %s to be invalid", tc.from, tc.to)
		}
	}
}

func TestCheckTransition_ReturnsCorrectError(t *testing.T) {
	// Valid transition → nil
	if err := checkTransition(StatusPending, StatusDelivered); err != nil {
		t.Errorf("expected nil for valid transition, got %v", err)
	}

	// Invalid from non-terminal → ErrInvalidStatus
	err := checkTransition(StatusPending, StatusArbitrating)
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("expected ErrInvalidStatus, got %v", err)
	}

	// From terminal state → ErrAlreadyResolved
	err = checkTransition(StatusReleased, StatusDisputed)
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Errorf("expected ErrAlreadyResolved, got %v", err)
	}

	err = checkTransition(StatusRefunded, StatusPending)
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Errorf("expected ErrAlreadyResolved, got %v", err)
	}

	err = checkTransition(StatusExpired, StatusReleased)
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Errorf("expected ErrAlreadyResolved, got %v", err)
	}
}

func TestIsTerminal(t *testing.T) {
	terminals := []Status{StatusReleased, StatusRefunded, StatusExpired}
	for _, s := range terminals {
		e := &Escrow{Status: s}
		if !e.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}

	nonTerminals := []Status{StatusPending, StatusDelivered, StatusDisputed, StatusArbitrating}
	for _, s := range nonTerminals {
		e := &Escrow{Status: s}
		if e.IsTerminal() {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}
