// Package tenant provides multi-tenancy for the Alancoin platform.
package tenant

import (
	"errors"
	"time"
)

// Errors
var (
	ErrTenantNotFound = errors.New("tenant: not found")
	ErrSlugTaken      = errors.New("tenant: slug already taken")
	ErrMaxAgents      = errors.New("tenant: maximum agents reached for plan")
)

// Status represents a tenant's lifecycle state.
type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
	StatusCancelled Status = "cancelled"
)

// Plan identifies the pricing tier.
type Plan string

const (
	PlanFree       Plan = "free"
	PlanStarter    Plan = "starter"
	PlanGrowth     Plan = "growth"
	PlanEnterprise Plan = "enterprise"
)

// Settings stores configurable tenant limits.
type Settings struct {
	RateLimitRPM     int      `json:"rateLimitRpm"`
	MaxAgents        int      `json:"maxAgents"`
	MaxSessionBudget string   `json:"maxSessionBudget"`
	AllowedOrigins   []string `json:"allowedOrigins,omitempty"`
	TakeRateBPS      int      `json:"takeRateBps"` // basis-point fee on settlement (0 = no fee)
}

// Tenant represents an organisation using the platform.
type Tenant struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Slug             string    `json:"slug"`
	Plan             Plan      `json:"plan"`
	StripeCustomerID string    `json:"stripeCustomerId,omitempty"`
	Status           Status    `json:"status"`
	Settings         Settings  `json:"settings"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}
