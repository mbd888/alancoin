package alancoin

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors for use with errors.Is().
var (
	ErrBudgetExceeded  = errors.New("alancoin: budget exceeded")
	ErrPolicyDenied    = errors.New("alancoin: policy denied")
	ErrSessionExpired  = errors.New("alancoin: session expired")
	ErrAgentNotFound   = errors.New("alancoin: agent not found")
	ErrServiceNotFound = errors.New("alancoin: service not found")
	ErrAgentExists     = errors.New("alancoin: agent already exists")
	ErrPaymentRequired = errors.New("alancoin: payment required")
	ErrUnauthorized    = errors.New("alancoin: unauthorized")
	ErrValidation      = errors.New("alancoin: validation error")
	ErrRateLimited     = errors.New("alancoin: rate limited")
	ErrServer          = errors.New("alancoin: server error")
	ErrNetwork         = errors.New("alancoin: network error")
)

// Error represents a structured error returned by the Alancoin API.
type Error struct {
	// StatusCode is the HTTP status code.
	StatusCode int `json:"statusCode,omitempty"`
	// Code is the machine-readable error code.
	Code string `json:"code,omitempty"`
	// Message is the human-readable error message.
	Message string `json:"message,omitempty"`
	// Details contains additional error context.
	Details map[string]any `json:"details,omitempty"`
	// Sentinel is the underlying sentinel error for errors.Is() matching.
	Sentinel error `json:"-"`
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("alancoin: %s: %s (HTTP %d)", e.Code, e.Message, e.StatusCode)
	}
	return fmt.Sprintf("alancoin: %s (HTTP %d)", e.Message, e.StatusCode)
}

func (e *Error) Unwrap() error {
	return e.Sentinel
}

// sentinelForStatus maps an HTTP status code to the appropriate sentinel error.
func sentinelForStatus(status int, code string) error {
	switch {
	case status == http.StatusBadRequest:
		return ErrValidation
	case status == http.StatusUnauthorized:
		return ErrUnauthorized
	case status == http.StatusPaymentRequired:
		return ErrPaymentRequired
	case status == http.StatusForbidden:
		return ErrPolicyDenied
	case status == http.StatusNotFound && code == "agent_not_found":
		return ErrAgentNotFound
	case status == http.StatusNotFound && code == "service_not_found":
		return ErrServiceNotFound
	case status == http.StatusNotFound:
		return ErrAgentNotFound
	case status == http.StatusConflict:
		return ErrAgentExists
	case status == http.StatusTooManyRequests:
		return ErrRateLimited
	case status >= 500:
		return ErrServer
	default:
		return nil
	}
}
