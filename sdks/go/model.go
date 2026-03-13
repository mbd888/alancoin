package alancoin

import "time"

// --- Agent & Service Models ---

// Service represents a service offered by an agent.
type Service struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Price       string `json:"price"`
	Description string `json:"description,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Active      bool   `json:"active"`
}

// AgentStats holds aggregate statistics for an agent.
type AgentStats struct {
	TotalReceived    string  `json:"totalReceived"`
	TotalSent        string  `json:"totalSent"`
	TransactionCount int     `json:"transactionCount"`
	SuccessRate      float64 `json:"successRate"`
}

// Agent represents a registered agent in the network.
type Agent struct {
	Address      string     `json:"address"`
	Name         string     `json:"name"`
	Description  string     `json:"description,omitempty"`
	OwnerAddress string     `json:"ownerAddress,omitempty"`
	IsAutonomous bool       `json:"isAutonomous,omitempty"`
	Endpoint     string     `json:"endpoint,omitempty"`
	Services     []Service  `json:"services,omitempty"`
	Stats        AgentStats `json:"stats,omitempty"`
	CreatedAt    time.Time  `json:"createdAt,omitempty"`
	UpdatedAt    time.Time  `json:"updatedAt,omitempty"`
}

// RegisterAgentRequest is the body for POST /v1/agents.
type RegisterAgentRequest struct {
	Address      string `json:"address"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	OwnerAddress string `json:"ownerAddress,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
}

// RegisterAgentResponse wraps the response from POST /v1/agents.
type RegisterAgentResponse struct {
	Agent  Agent  `json:"agent"`
	APIKey string `json:"apiKey"`
	KeyID  string `json:"keyId"`
	Usage  string `json:"usage"`
}

// ListAgentsResponse wraps GET /v1/agents.
type ListAgentsResponse struct {
	Agents []Agent `json:"agents"`
}

// --- Service CRUD ---

// AddServiceRequest is the body for POST /v1/agents/:addr/services.
type AddServiceRequest struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Price       string `json:"price"`
	Description string `json:"description,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
}

// UpdateServiceRequest is the body for PUT /v1/agents/:addr/services/:id.
type UpdateServiceRequest struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Price       string `json:"price,omitempty"`
	Description string `json:"description,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Active      *bool  `json:"active,omitempty"`
}

// --- Discovery ---

// ServiceListing is a service with agent info and reputation context.
type ServiceListing struct {
	ID               string  `json:"id"`
	Type             string  `json:"type"`
	Name             string  `json:"name"`
	Price            string  `json:"price"`
	Description      string  `json:"description,omitempty"`
	Endpoint         string  `json:"endpoint,omitempty"`
	Active           bool    `json:"active"`
	AgentAddress     string  `json:"agentAddress"`
	AgentName        string  `json:"agentName"`
	ReputationScore  float64 `json:"reputationScore"`
	ReputationTier   string  `json:"reputationTier"`
	SuccessRate      float64 `json:"successRate"`
	TransactionCount int     `json:"transactionCount"`
	ValueScore       float64 `json:"valueScore"`
}

// DiscoverResponse wraps GET /v1/services.
type DiscoverResponse struct {
	Services []ServiceListing `json:"services"`
}

// DiscoverOptions configures the discovery filter.
type DiscoverOptions struct {
	Type     string
	MinPrice string
	MaxPrice string
	SortBy   string
	Limit    int
	Offset   int
}

// --- Transactions ---

// Transaction represents a payment between agents.
type Transaction struct {
	ID        string    `json:"id"`
	TxHash    string    `json:"txHash"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Amount    string    `json:"amount"`
	ServiceID string    `json:"serviceId,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// --- Network ---

// NetworkStats holds network-wide statistics.
type NetworkStats struct {
	TotalAgents       int    `json:"totalAgents"`
	TotalServices     int    `json:"totalServices"`
	TotalTransactions int    `json:"totalTransactions"`
	TotalVolume       string `json:"totalVolume"`
}

// --- Reputation ---

// Reputation holds an agent's computed reputation.
type Reputation struct {
	Score      float64            `json:"score"`
	Tier       string             `json:"tier"`
	Components map[string]float64 `json:"components,omitempty"`
	Metrics    map[string]any     `json:"metrics,omitempty"`
}

// ReputationResponse wraps GET /v1/reputation/:address.
type ReputationResponse struct {
	Reputation Reputation `json:"reputation"`
}

// BatchReputationEntry is one entry in a batch reputation response.
type BatchReputationEntry struct {
	Address    string     `json:"address"`
	Reputation Reputation `json:"reputation"`
}

// BatchReputationResponse wraps POST /v1/reputation/batch.
type BatchReputationResponse struct {
	Scores []BatchReputationEntry `json:"scores"`
}

// CompareAgentsEntry is one entry in a comparison response.
type CompareAgentsEntry struct {
	Address string  `json:"address"`
	Score   float64 `json:"score"`
}

// CompareAgentsResponse wraps POST /v1/reputation/compare.
type CompareAgentsResponse struct {
	Best   string               `json:"best"`
	Agents []CompareAgentsEntry `json:"agents"`
}

// ReputationSnapshot is a point-in-time reputation score.
type ReputationSnapshot struct {
	ID                string    `json:"id"`
	Address           string    `json:"address"`
	Score             float64   `json:"score"`
	Tier              string    `json:"tier"`
	VolumeScore       float64   `json:"volumeScore"`
	ActivityScore     float64   `json:"activityScore"`
	SuccessScore      float64   `json:"successScore"`
	AgeScore          float64   `json:"ageScore"`
	DiversityScore    float64   `json:"diversityScore"`
	TotalTransactions int       `json:"totalTransactions"`
	TotalVolume       string    `json:"totalVolume"`
	SuccessRate       float64   `json:"successRate"`
	UniquePeers       int       `json:"uniquePeers"`
	CreatedAt         time.Time `json:"createdAt"`
}

// HistoryResponse wraps GET /v1/reputation/:address/history.
type HistoryResponse struct {
	Snapshots []ReputationSnapshot `json:"snapshots"`
}

// LeaderboardEntry is one entry on the reputation leaderboard.
type LeaderboardEntry struct {
	Address string  `json:"address"`
	Score   float64 `json:"score"`
	Tier    string  `json:"tier"`
}

// LeaderboardResponse wraps GET /v1/reputation.
type LeaderboardResponse struct {
	Leaderboard []LeaderboardEntry `json:"leaderboard"`
	Total       int                `json:"total"`
	Tiers       map[string]int     `json:"tiers,omitempty"`
}

// --- Ledger ---

// Balance holds an agent's current fund balances.
type Balance struct {
	Available string `json:"available"`
	Pending   string `json:"pending"`
	TotalIn   string `json:"totalIn"`
	TotalOut  string `json:"totalOut"`
}

// BalanceResponse wraps GET /v1/agents/:addr/balance.
type BalanceResponse struct {
	Balance Balance `json:"balance"`
}

// LedgerEntry is a single ledger record.
type LedgerEntry struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Amount    string    `json:"amount"`
	Balance   string    `json:"balance"`
	Reference string    `json:"reference,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// LedgerResponse wraps GET /v1/agents/:addr/ledger.
type LedgerResponse struct {
	Entries []LedgerEntry `json:"entries"`
}

// WithdrawalResponse wraps POST /v1/agents/:addr/withdraw.
type WithdrawalResponse struct {
	Status     string         `json:"status"`
	Withdrawal map[string]any `json:"withdrawal"`
}

// --- Gateway ---

// GatewayConfig configures a new gateway session.
type GatewayConfig struct {
	MaxTotal             string   `json:"maxTotal"`
	MaxPerRequest        string   `json:"maxPerRequest,omitempty"`
	Strategy             string   `json:"strategy,omitempty"`
	AllowedTypes         []string `json:"allowedTypes,omitempty"`
	ExpiresInSecs        int      `json:"expiresInSecs,omitempty"`
	WarnAtPercent        int      `json:"warnAtPercent,omitempty"`
	MaxRequestsPerMinute int      `json:"maxRequestsPerMinute,omitempty"`
}

// GatewaySessionInfo holds the state of a gateway session.
type GatewaySessionInfo struct {
	ID                   string    `json:"id"`
	AgentAddr            string    `json:"agentAddr"`
	TenantID             string    `json:"tenantId,omitempty"`
	MaxTotal             string    `json:"maxTotal"`
	MaxPerRequest        string    `json:"maxPerRequest"`
	TotalSpent           string    `json:"totalSpent"`
	RequestCount         int       `json:"requestCount"`
	Strategy             string    `json:"strategy"`
	AllowedTypes         []string  `json:"allowedTypes,omitempty"`
	WarnAtPercent        int       `json:"warnAtPercent,omitempty"`
	MaxRequestsPerMinute int       `json:"maxRequestsPerMinute,omitempty"`
	Status               string    `json:"status"`
	ExpiresAt            time.Time `json:"expiresAt"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// createSessionResponse wraps POST /v1/gateway/sessions.
type createSessionResponse struct {
	Session GatewaySessionInfo `json:"session"`
}

// ProxyRequest is the body for POST /v1/gateway/proxy.
type ProxyRequest struct {
	ServiceType    string         `json:"serviceType"`
	Params         map[string]any `json:"params,omitempty"`
	MaxPrice       string         `json:"maxPrice,omitempty"`
	PreferAgent    string         `json:"preferAgent,omitempty"`
	IdempotencyKey string         `json:"idempotencyKey,omitempty"`
}

// ProxyResult is the response from a proxy call.
type ProxyResult struct {
	Response    map[string]any `json:"response"`
	ServiceUsed string         `json:"serviceUsed"`
	ServiceName string         `json:"serviceName"`
	AmountPaid  string         `json:"amountPaid"`
	TotalSpent  string         `json:"totalSpent"`
	Remaining   string         `json:"remaining"`
	BudgetLow   bool           `json:"budgetLow,omitempty"`
	LatencyMs   int64          `json:"latencyMs"`
	Retries     int            `json:"retries"`
}

// PipelineStep defines one step in a gateway pipeline.
type PipelineStep struct {
	ServiceType string         `json:"serviceType"`
	Params      map[string]any `json:"params,omitempty"`
	MaxPrice    string         `json:"maxPrice,omitempty"`
}

// PipelineStepResult is the result of a single pipeline step.
type PipelineStepResult struct {
	StepIndex   int            `json:"stepIndex"`
	ServiceType string         `json:"serviceType"`
	Response    map[string]any `json:"response"`
	ServiceUsed string         `json:"serviceUsed"`
	ServiceName string         `json:"serviceName"`
	AmountPaid  string         `json:"amountPaid"`
	LatencyMs   int64          `json:"latencyMs"`
}

// PipelineResult is the response from a pipeline execution.
type PipelineResult struct {
	Steps      []PipelineStepResult `json:"steps"`
	TotalPaid  string               `json:"totalPaid"`
	TotalSpent string               `json:"totalSpent"`
	Remaining  string               `json:"remaining"`
}

// SingleCallRequest is the body for POST /v1/gateway/call.
type SingleCallRequest struct {
	MaxPrice    string         `json:"maxPrice"`
	ServiceType string         `json:"serviceType"`
	Params      map[string]any `json:"params,omitempty"`
}

// SingleCallResult is the response from a single call.
type SingleCallResult struct {
	Response    map[string]any `json:"response"`
	ServiceUsed string         `json:"serviceUsed"`
	ServiceName string         `json:"serviceName"`
	AmountPaid  string         `json:"amountPaid"`
	LatencyMs   int64          `json:"latencyMs"`
}

// RequestLog is a recorded proxy attempt.
type RequestLog struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	TenantID    string    `json:"tenantId,omitempty"`
	ServiceType string    `json:"serviceType"`
	AgentCalled string    `json:"agentCalled"`
	Amount      string    `json:"amount"`
	FeeAmount   string    `json:"feeAmount,omitempty"`
	Status      string    `json:"status"`
	LatencyMs   int64     `json:"latencyMs"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// closeSessionResponse wraps DELETE /v1/gateway/sessions/:id.
type closeSessionResponse struct {
	Session GatewaySessionInfo `json:"session"`
}

// listSessionsResponse wraps GET /v1/gateway/sessions.
type listSessionsResponse struct {
	Sessions []GatewaySessionInfo `json:"sessions"`
}

// listLogsResponse wraps GET /v1/gateway/sessions/:id/logs.
type listLogsResponse struct {
	Logs []RequestLog `json:"logs"`
}

// --- Webhooks ---

// Webhook represents a registered webhook.
type Webhook struct {
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Secret string   `json:"secret,omitempty"`
}

// CreateWebhookRequest is the body for POST /v1/agents/:addr/webhooks.
type CreateWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// createWebhookResponse wraps POST /v1/agents/:addr/webhooks.
type createWebhookResponse struct {
	ID     string   `json:"id"`
	Secret string   `json:"secret"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// listWebhooksResponse wraps GET /v1/agents/:addr/webhooks.
type listWebhooksResponse struct {
	Webhooks []Webhook `json:"webhooks"`
}

// --- Service Type Constants ---

const (
	ServiceTypeInference   = "inference"
	ServiceTypeEmbedding   = "embedding"
	ServiceTypeTranslation = "translation"
	ServiceTypeCode        = "code"
	ServiceTypeData        = "data"
	ServiceTypeImage       = "image"
	ServiceTypeAudio       = "audio"
	ServiceTypeSearch      = "search"
	ServiceTypeCompute     = "compute"
	ServiceTypeStorage     = "storage"
	ServiceTypeOther       = "other"
)
