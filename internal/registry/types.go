// Package registry implements agent registration and discovery
// This is the network layer - the thing that creates the moat
package registry

import (
	"errors"
	"time"
)

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

var (
	ErrAgentNotFound      = errors.New("registry: agent not found")
	ErrAgentExists        = errors.New("registry: agent already registered")
	ErrServiceNotFound    = errors.New("registry: service not found")
	ErrInvalidAddress     = errors.New("registry: invalid wallet address")
	ErrInvalidServiceType = errors.New("registry: invalid service type")
)

// -----------------------------------------------------------------------------
// Core Types
// -----------------------------------------------------------------------------

// Agent represents a registered AI agent in the network
type Agent struct {
	// Identity
	Address     string `json:"address"`     // Wallet address (primary key)
	Name        string `json:"name"`        // Human-readable name
	Description string `json:"description"` // What this agent does

	// Owner info (for session key agents)
	OwnerAddress string `json:"ownerAddress,omitempty"` // Human owner's wallet
	IsAutonomous bool   `json:"isAutonomous"`           // Has session key permissions

	// Services this agent offers
	Services []Service `json:"services"`

	// Metadata
	Endpoint  string                 `json:"endpoint,omitempty"` // x402-compatible API endpoint
	Metadata  map[string]interface{} `json:"metadata,omitempty"` // Additional metadata
	CreatedAt time.Time              `json:"createdAt"`
	UpdatedAt time.Time              `json:"updatedAt"`

	// Stats (the data moat)
	Stats AgentStats `json:"stats"`
}

// Service represents something an agent offers
type Service struct {
	ID           string                 `json:"id"`                     // Unique within agent
	Type         string                 `json:"type"`                   // Category: "inference", "translation", "code", "data", etc.
	Name         string                 `json:"name"`                   // Human-readable name
	Description  string                 `json:"description"`            // What this service does
	Price        string                 `json:"price"`                  // USDC price per call
	Endpoint     string                 `json:"endpoint"`               // API endpoint for this service
	Active       bool                   `json:"active"`                 // Currently available
	AgentAddress string                 `json:"agentAddress,omitempty"` // Owning agent's address
	Metadata     map[string]interface{} `json:"metadata,omitempty"`     // Additional metadata
}

// AgentStats tracks agent activity (this becomes reputation)
type AgentStats struct {
	TotalReceived      string     `json:"totalReceived"`        // Total USDC received
	TotalSent          string     `json:"totalSent"`            // Total USDC sent
	TotalSpent         string     `json:"totalSpent,omitempty"` // Total USDC spent (alias for TotalSent)
	TransactionCount   int64      `json:"transactionCount"`     // Number of transactions
	SuccessRate        float64    `json:"successRate"`          // Successful transactions / total
	FirstTransactionAt *time.Time `json:"firstTransactionAt,omitempty"`
	LastTransactionAt  *time.Time `json:"lastTransactionAt,omitempty"`
	LastActive         time.Time  `json:"lastActive,omitempty"` // Last activity timestamp
}

// -----------------------------------------------------------------------------
// Registration Types
// -----------------------------------------------------------------------------

// RegisterAgentRequest is the payload for agent registration
type RegisterAgentRequest struct {
	Address      string `json:"address" binding:"required"`
	Name         string `json:"name" binding:"required"`
	Description  string `json:"description"`
	OwnerAddress string `json:"ownerAddress,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
}

// AddServiceRequest is the payload for adding a service
type AddServiceRequest struct {
	Type        string `json:"type" binding:"required"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Price       string `json:"price" binding:"required"`
	Endpoint    string `json:"endpoint"`
}

// -----------------------------------------------------------------------------
// Query Types
// -----------------------------------------------------------------------------

// AgentQuery filters for listing agents
type AgentQuery struct {
	ServiceType string // Filter by service type
	MinPrice    string // Minimum price
	MaxPrice    string // Maximum price
	Active      *bool  // Only active services
	Limit       int    // Max results (default 100)
	Offset      int    // Pagination offset
}

// AgentFilter is an alias for AgentQuery (PostgresStore compatibility)
type AgentFilter = AgentQuery

// ServiceFilter for service discovery queries
type ServiceFilter struct {
	Type     string
	MinPrice string
	MaxPrice string
	Limit    int
	Offset   int
}

// ServiceListing is a service with its agent info (for discovery)
type ServiceListing struct {
	Service
	AgentAddress    string  `json:"agentAddress"`
	AgentName       string  `json:"agentName"`
	ReputationScore float64 `json:"reputationScore"`      // 0-100
	ReputationTier  string  `json:"reputationTier"`       // new/emerging/established/trusted/elite
	SuccessRate     float64 `json:"successRate"`          // 0-1
	TxCount         int64   `json:"transactionCount"`     // Total transactions for this agent
	ValueScore      float64 `json:"valueScore,omitempty"` // Composite: reputation vs price
}

// -----------------------------------------------------------------------------
// Transaction Recording (the data moat)
// -----------------------------------------------------------------------------

// Transaction records a payment between agents
type Transaction struct {
	ID        string                 `json:"id"`
	TxHash    string                 `json:"txHash"`             // On-chain transaction
	From      string                 `json:"from"`               // Payer agent address
	To        string                 `json:"to"`                 // Payee agent address
	Amount    string                 `json:"amount"`             // USDC amount
	ServiceID string                 `json:"serviceId"`          // Which service was paid for
	Status    string                 `json:"status"`             // "pending", "confirmed", "failed"
	Metadata  map[string]interface{} `json:"metadata,omitempty"` // Additional transaction metadata
	CreatedAt time.Time              `json:"createdAt"`
}

// -----------------------------------------------------------------------------
// Service Types (the taxonomy)
// -----------------------------------------------------------------------------

// Known service types - agents can use these or define their own
var KnownServiceTypes = []string{
	"inference",   // LLM inference
	"embedding",   // Text embeddings
	"translation", // Language translation
	"code",        // Code generation/review
	"data",        // Data retrieval/processing
	"image",       // Image generation/analysis
	"audio",       // Audio transcription/generation
	"search",      // Web/database search
	"compute",     // General compute
	"storage",     // Data storage
	"other",       // Catch-all
}

// IsKnownServiceType checks if a service type is in our taxonomy
func IsKnownServiceType(t string) bool {
	for _, known := range KnownServiceTypes {
		if known == t {
			return true
		}
	}
	return false
}
