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

// --- TraceRank ---

// TraceRankScore holds an agent's graph-based reputation score.
type TraceRankScore struct {
	Address      string    `json:"address"`
	GraphScore   float64   `json:"graphScore"`
	RawRank      float64   `json:"rawRank"`
	SeedSignal   float64   `json:"seedSignal"`
	InDegree     int       `json:"inDegree"`
	OutDegree    int       `json:"outDegree"`
	InVolume     float64   `json:"inVolume"`
	OutVolume    float64   `json:"outVolume"`
	Iterations   int       `json:"iterations"`
	ComputedAt   time.Time `json:"computedAt"`
	ComputeRunID string    `json:"computeRunId"`
}

// traceRankLeaderboardResponse wraps GET /v1/tracerank/leaderboard.
type traceRankLeaderboardResponse struct {
	Agents []TraceRankScore `json:"agents"`
	Count  int              `json:"count"`
}

// TraceRankRun holds metadata about a TraceRank computation run.
type TraceRankRun struct {
	RunID      string  `json:"runId"`
	NodeCount  int     `json:"nodeCount"`
	EdgeCount  int     `json:"edgeCount"`
	Iterations int     `json:"iterations"`
	Converged  bool    `json:"converged"`
	DurationMs int64   `json:"durationMs"`
	MaxScore   float64 `json:"maxScore"`
	MeanScore  float64 `json:"meanScore"`
	ComputedAt string  `json:"computedAt"`
}

// traceRankRunsResponse wraps GET /v1/tracerank/runs.
type traceRankRunsResponse struct {
	Runs  []TraceRankRun `json:"runs"`
	Count int            `json:"count"`
}

// --- Flywheel ---

// FlywheelHealth is the condensed health response.
type FlywheelHealth struct {
	HealthScore        float64 `json:"healthScore"`
	HealthTier         string  `json:"healthTier"`
	VelocityScore      float64 `json:"velocityScore"`
	GrowthScore        float64 `json:"growthScore"`
	DensityScore       float64 `json:"densityScore"`
	EffectivenessScore float64 `json:"effectivenessScore"`
	RetentionScore     float64 `json:"retentionScore"`
}

// FlywheelState is the full flywheel state snapshot.
type FlywheelState struct {
	// Velocity
	TransactionsPerHour float64 `json:"transactionsPerHour"`
	VolumePerHourUSD    float64 `json:"volumePerHourUsd"`
	// Growth
	TxGrowthRatePct     float64 `json:"txGrowthRatePct"`
	VolumeGrowthRatePct float64 `json:"volumeGrowthRatePct"`
	NewAgents7d         int     `json:"newAgents7d"`
	AgentGrowthRatePct  float64 `json:"agentGrowthRatePct"`
	// Density
	TotalAgents  int     `json:"totalAgents"`
	ActiveAgents int     `json:"activeAgents7d"`
	TotalEdges   int     `json:"totalEdges"`
	GraphDensity float64 `json:"graphDensity"`
	AvgDegree    float64 `json:"avgDegree"`
	Reciprocity  float64 `json:"reciprocity"`
	// Effectiveness
	TierDistribution      map[string]int `json:"tierDistribution"`
	TopTierTrafficShare   float64        `json:"topTierTrafficShare"`
	ReputationCorrelation float64        `json:"reputationCorrelation"`
	// Retention
	RetentionRate7d float64 `json:"retentionRate7d"`
	ChurnRate7d     float64 `json:"churnRate7d"`
	// Scores
	HealthScore        float64   `json:"healthScore"`
	HealthTier         string    `json:"healthTier"`
	VelocityScore      float64   `json:"velocityScore"`
	GrowthScore        float64   `json:"growthScore"`
	DensityScore       float64   `json:"densityScore"`
	EffectivenessScore float64   `json:"effectivenessScore"`
	RetentionScore     float64   `json:"retentionScore"`
	ComputedAt         time.Time `json:"computedAt"`
}

// FlywheelHistoryEntry is a condensed history point.
type FlywheelHistoryEntry struct {
	HealthScore float64   `json:"healthScore"`
	HealthTier  string    `json:"healthTier"`
	TxPerHour   float64   `json:"txPerHour"`
	Agents      int       `json:"agents"`
	ComputedAt  time.Time `json:"computedAt"`
}

// flywheelHistoryResponse wraps GET /v1/flywheel/history.
type flywheelHistoryResponse struct {
	History []FlywheelHistoryEntry `json:"history"`
}

// --- Gateway Extensions ---

// PolicyDecision holds the result of a spend policy evaluation.
type PolicyDecision struct {
	Evaluated  int    `json:"evaluated"`
	Allowed    bool   `json:"allowed"`
	DeniedBy   string `json:"deniedBy,omitempty"`
	DeniedRule string `json:"deniedRule,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Shadow     bool   `json:"shadow,omitempty"`
	LatencyUs  int64  `json:"latencyUs"`
}

// DryRunRequest is the body for POST /v1/gateway/sessions/:id/dry-run.
type DryRunRequest struct {
	ServiceType string         `json:"serviceType"`
	Params      map[string]any `json:"params,omitempty"`
	MaxPrice    string         `json:"maxPrice,omitempty"`
	PreferAgent string         `json:"preferAgent,omitempty"`
}

// DryRunResult is the response from a dry-run check.
type DryRunResult struct {
	Allowed      bool            `json:"allowed"`
	PolicyResult *PolicyDecision `json:"policyResult,omitempty"`
	BudgetOk     bool            `json:"budgetOk"`
	Remaining    string          `json:"remaining"`
	ServiceFound bool            `json:"serviceFound"`
	BestPrice    string          `json:"bestPrice,omitempty"`
	BestService  string          `json:"bestService,omitempty"`
	DenyReason   string          `json:"denyReason,omitempty"`
}

// dryRunResponse wraps the dry-run endpoint response.
type dryRunResponse struct {
	Result DryRunResult `json:"result"`
}

// listTransactionsResponse wraps GET /v1/agents/:addr/transactions.
type listTransactionsResponse struct {
	Transactions []Transaction `json:"transactions"`
	Count        int           `json:"count"`
}

// --- Streaming Micropayments ---

// Stream represents a streaming micropayment channel.
type Stream struct {
	ID              string     `json:"id"`
	BuyerAddr       string     `json:"buyerAddr"`
	SellerAddr      string     `json:"sellerAddr"`
	ServiceID       string     `json:"serviceId,omitempty"`
	SessionKeyID    string     `json:"sessionKeyId,omitempty"`
	HoldAmount      string     `json:"holdAmount"`
	SpentAmount     string     `json:"spentAmount"`
	PricePerTick    string     `json:"pricePerTick"`
	TickCount       int        `json:"tickCount"`
	Status          string     `json:"status"`
	StaleTimeoutSec int        `json:"staleTimeoutSecs"`
	LastTickAt      *time.Time `json:"lastTickAt,omitempty"`
	ClosedAt        *time.Time `json:"closedAt,omitempty"`
	CloseReason     string     `json:"closeReason,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// StreamTick represents a single micropayment tick.
type StreamTick struct {
	ID         string    `json:"id"`
	StreamID   string    `json:"streamId"`
	Seq        int       `json:"seq"`
	Amount     string    `json:"amount"`
	Cumulative string    `json:"cumulative"`
	Metadata   string    `json:"metadata,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// OpenStreamRequest is the body for POST /v1/streams.
type OpenStreamRequest struct {
	BuyerAddr       string `json:"buyerAddr"`
	SellerAddr      string `json:"sellerAddr"`
	HoldAmount      string `json:"holdAmount"`
	PricePerTick    string `json:"pricePerTick"`
	ServiceID       string `json:"serviceId,omitempty"`
	SessionKeyID    string `json:"sessionKeyId,omitempty"`
	StaleTimeoutSec int    `json:"staleTimeoutSecs,omitempty"`
}

// TickStreamRequest is the body for POST /v1/streams/:id/tick.
type TickStreamRequest struct {
	Seq      int    `json:"seq,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Metadata string `json:"metadata,omitempty"`
}

// streamResponse wraps stream endpoint responses.
type streamResponse struct {
	Stream Stream `json:"stream"`
}

// tickResponse wraps POST /v1/streams/:id/tick.
type tickResponse struct {
	Tick   StreamTick `json:"tick"`
	Stream Stream     `json:"stream"`
}

// listTicksResponse wraps GET /v1/streams/:id/ticks.
type listTicksResponse struct {
	Ticks []StreamTick `json:"ticks"`
}

// listStreamsResponse wraps GET /v1/agents/:addr/streams.
type listStreamsResponse struct {
	Streams []Stream `json:"streams"`
}

// --- Escrow (Buyer Protection) ---

// Escrow represents a buyer-protection escrow record.
type Escrow struct {
	ID                   string          `json:"id"`
	BuyerAddr            string          `json:"buyerAddr"`
	SellerAddr           string          `json:"sellerAddr"`
	Amount               string          `json:"amount"`
	ServiceID            string          `json:"serviceId,omitempty"`
	SessionKeyID         string          `json:"sessionKeyId,omitempty"`
	Status               string          `json:"status"`
	AutoReleaseAt        time.Time       `json:"autoReleaseAt"`
	DeliveredAt          *time.Time      `json:"deliveredAt,omitempty"`
	ResolvedAt           *time.Time      `json:"resolvedAt,omitempty"`
	DisputeReason        string          `json:"disputeReason,omitempty"`
	Resolution           string          `json:"resolution,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
	DisputeEvidence      []EvidenceEntry `json:"disputeEvidence,omitempty"`
	ArbitratorAddr       string          `json:"arbitratorAddr,omitempty"`
	ArbitrationDeadline  *time.Time      `json:"arbitrationDeadline,omitempty"`
	PartialReleaseAmount string          `json:"partialReleaseAmount,omitempty"`
}

// EvidenceEntry is a piece of dispute evidence.
type EvidenceEntry struct {
	SubmittedBy string    `json:"submittedBy"`
	Content     string    `json:"content"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// CreateEscrowRequest is the body for POST /v1/escrow.
type CreateEscrowRequest struct {
	BuyerAddr    string `json:"buyerAddr"`
	SellerAddr   string `json:"sellerAddr"`
	Amount       string `json:"amount"`
	ServiceID    string `json:"serviceId,omitempty"`
	SessionKeyID string `json:"sessionKeyId,omitempty"`
	AutoRelease  string `json:"autoRelease,omitempty"`
}

// escrowResponse wraps escrow endpoint responses.
type escrowResponse struct {
	Escrow Escrow `json:"escrow"`
}

// listEscrowsResponse wraps GET /v1/agents/:addr/escrows.
type listEscrowsResponse struct {
	Escrows []Escrow `json:"escrows"`
}

// --- Session Keys (Bounded Autonomy) ---

// SessionKeyPermission defines the spending constraints for a session key.
type SessionKeyPermission struct {
	MaxPerTransaction    string    `json:"maxPerTransaction,omitempty"`
	MaxPerDay            string    `json:"maxPerDay,omitempty"`
	MaxTotal             string    `json:"maxTotal,omitempty"`
	ValidAfter           time.Time `json:"validAfter,omitempty"`
	ExpiresAt            time.Time `json:"expiresAt"`
	AllowedRecipients    []string  `json:"allowedRecipients,omitempty"`
	AllowedServiceTypes  []string  `json:"allowedServiceTypes,omitempty"`
	AllowedServiceAgents []string  `json:"allowedServiceAgents,omitempty"`
	AllowAny             bool      `json:"allowAny,omitempty"`
	Scopes               []string  `json:"scopes,omitempty"`
	Label                string    `json:"label,omitempty"`
}

// SessionKeyUsage tracks how a session key has been used.
type SessionKeyUsage struct {
	TransactionCount int       `json:"transactionCount"`
	TotalSpent       string    `json:"totalSpent"`
	SpentToday       string    `json:"spentToday"`
	LastUsed         time.Time `json:"lastUsed,omitempty"`
	LastResetDay     string    `json:"lastResetDay,omitempty"`
	LastNonce        uint64    `json:"lastNonce"`
}

// SessionKey represents a bounded-autonomy session key.
type SessionKey struct {
	ID               string               `json:"id"`
	OwnerAddr        string               `json:"ownerAddr"`
	PublicKey        string               `json:"publicKey"`
	CreatedAt        time.Time            `json:"createdAt"`
	RevokedAt        *time.Time           `json:"revokedAt,omitempty"`
	Permission       SessionKeyPermission `json:"permission"`
	Usage            SessionKeyUsage      `json:"usage"`
	ParentKeyID      string               `json:"parentKeyId,omitempty"`
	Depth            int                  `json:"depth"`
	RootKeyID        string               `json:"rootKeyId,omitempty"`
	DelegationLabel  string               `json:"delegationLabel,omitempty"`
	RotatedFromID    string               `json:"rotatedFromId,omitempty"`
	RotatedToID      string               `json:"rotatedToId,omitempty"`
	RotationGraceEnd *time.Time           `json:"rotationGraceEnd,omitempty"`
	DelegationProof  *SDKDelegationProof  `json:"delegationProof,omitempty"`
}

// SDKDelegationProof is the client-side representation of a macaroon-inspired
// HMAC-chain delegation proof. When present on a session key, it enables
// O(1) verification of the entire delegation ancestry without database walks.
type SDKDelegationProof struct {
	Caveats   []SDKCaveat `json:"caveats"`
	Tag       string      `json:"tag"`
	RootKeyID string      `json:"rootKeyId"`
}

// SDKCaveat encodes a single permission restriction in a delegation chain.
type SDKCaveat struct {
	MaxTotal            string    `json:"maxTotal,omitempty"`
	MaxPerTransaction   string    `json:"maxPerTx,omitempty"`
	MaxPerDay           string    `json:"maxPerDay,omitempty"`
	ExpiresAt           time.Time `json:"expiresAt"`
	AllowedRecipients   []string  `json:"recipients,omitempty"`
	AllowedServiceTypes []string  `json:"serviceTypes,omitempty"`
	Scopes              []string  `json:"scopes,omitempty"`
	PublicKey           string    `json:"publicKey"`
	KeyID               string    `json:"keyId"`
	Depth               int       `json:"depth"`
	IssuedAt            time.Time `json:"issuedAt"`
	IssuerID            string    `json:"issuerId,omitempty"`
}

// CreateSessionKeyRequest is the body for POST /v1/agents/:addr/sessions.
type CreateSessionKeyRequest struct {
	PublicKey           string   `json:"publicKey"`
	MaxPerTransaction   string   `json:"maxPerTransaction,omitempty"`
	MaxPerDay           string   `json:"maxPerDay,omitempty"`
	MaxTotal            string   `json:"maxTotal,omitempty"`
	ExpiresIn           string   `json:"expiresIn,omitempty"`
	ExpiresAt           string   `json:"expiresAt,omitempty"`
	AllowedRecipients   []string `json:"allowedRecipients,omitempty"`
	AllowedServiceTypes []string `json:"allowedServiceTypes,omitempty"`
	AllowAny            bool     `json:"allowAny,omitempty"`
	Scopes              []string `json:"scopes,omitempty"`
	Label               string   `json:"label,omitempty"`
}

// createSessionKeyResponse wraps POST /v1/agents/:addr/sessions.
type createSessionKeyResponse struct {
	ID         string               `json:"id"`
	Permission SessionKeyPermission `json:"permissions"`
	Usage      SessionKeyUsage      `json:"usage"`
}

// listSessionKeysResponse wraps GET /v1/agents/:addr/sessions.
type listSessionKeysResponse struct {
	Sessions []SessionKey `json:"sessions"`
}

// getSessionKeyResponse wraps GET /v1/agents/:addr/sessions/:keyId.
type getSessionKeyResponse struct {
	Session SessionKey `json:"session"`
}

// TransactRequest is the body for POST /v1/agents/:addr/sessions/:keyId/transact.
type TransactRequest struct {
	To        string `json:"to"`
	Amount    string `json:"amount"`
	ServiceID string `json:"serviceId,omitempty"`
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}

// TransactResponse wraps the response from a session key transaction.
type TransactResponse struct {
	TxHash       string    `json:"txHash"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Amount       string    `json:"amount"`
	SessionKeyID string    `json:"sessionKeyId"`
	Timestamp    time.Time `json:"timestamp"`
}

// DelegateRequest is the body for POST /v1/sessions/:keyId/delegate.
type DelegateRequest struct {
	PublicKey           string   `json:"publicKey"`
	MaxTotal            string   `json:"maxTotal"`
	MaxPerTransaction   string   `json:"maxPerTransaction,omitempty"`
	MaxPerDay           string   `json:"maxPerDay,omitempty"`
	ExpiresIn           string   `json:"expiresIn,omitempty"`
	AllowedRecipients   []string `json:"allowedRecipients,omitempty"`
	AllowedServiceTypes []string `json:"allowedServiceTypes,omitempty"`
	AllowAny            bool     `json:"allowAny,omitempty"`
	Scopes              []string `json:"scopes,omitempty"`
	DelegationLabel     string   `json:"delegationLabel,omitempty"`
	Nonce               uint64   `json:"nonce"`
	Timestamp           int64    `json:"timestamp"`
	Signature           string   `json:"signature"`
}

// DelegationTreeNode represents a node in a delegation hierarchy.
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

// delegationTreeResponse wraps GET /v1/sessions/:keyId/tree.
type delegationTreeResponse struct {
	Tree DelegationTreeNode `json:"tree"`
}

// DelegationLogEntry is an audit record for delegation events.
type DelegationLogEntry struct {
	ID            int       `json:"id"`
	ParentKeyID   string    `json:"parentKeyId"`
	ChildKeyID    string    `json:"childKeyId"`
	RootKeyID     string    `json:"rootKeyId"`
	RootOwnerAddr string    `json:"rootOwnerAddr"`
	Depth         int       `json:"depth"`
	MaxTotal      string    `json:"maxTotal,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	EventType     string    `json:"eventType"`
	AncestorChain []string  `json:"ancestorChain,omitempty"`
	Metadata      string    `json:"metadata,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// delegationLogResponse wraps GET /v1/sessions/:keyId/delegation-log.
type delegationLogResponse struct {
	Log []DelegationLogEntry `json:"log"`
}

// --- MultiStep Escrow ---

// PlannedStep is a single step in a multistep escrow plan.
type PlannedStep struct {
	SellerAddr string `json:"sellerAddr"`
	Amount     string `json:"amount"`
}

// MultiStepEscrow holds funds for an N-step pipeline with per-step release.
type MultiStepEscrow struct {
	ID             string        `json:"id"`
	BuyerAddr      string        `json:"buyerAddr"`
	TotalAmount    string        `json:"totalAmount"`
	SpentAmount    string        `json:"spentAmount"`
	TotalSteps     int           `json:"totalSteps"`
	ConfirmedSteps int           `json:"confirmedSteps"`
	PlannedSteps   []PlannedStep `json:"plannedSteps"`
	Status         string        `json:"status"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
}

// CreateMultiStepEscrowRequest is the body for POST /v1/escrow/multistep.
type CreateMultiStepEscrowRequest struct {
	TotalAmount  string        `json:"totalAmount"`
	TotalSteps   int           `json:"totalSteps"`
	PlannedSteps []PlannedStep `json:"plannedSteps"`
}

// ConfirmStepRequest is the body for POST /v1/escrow/multistep/:id/confirm-step.
type ConfirmStepRequest struct {
	StepIndex  int    `json:"stepIndex"`
	SellerAddr string `json:"sellerAddr"`
	Amount     string `json:"amount"`
}

// multiStepEscrowResponse wraps multistep escrow endpoint responses.
type multiStepEscrowResponse struct {
	Escrow MultiStepEscrow `json:"escrow"`
}

// --- Receipts ---

// Receipt is an HMAC-signed payment proof.
type Receipt struct {
	ID          string    `json:"id"`
	PaymentPath string    `json:"paymentPath"`
	Reference   string    `json:"reference"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	Amount      string    `json:"amount"`
	ServiceID   string    `json:"serviceId,omitempty"`
	Status      string    `json:"status"`
	PayloadHash string    `json:"payloadHash"`
	Signature   string    `json:"signature"`
	IssuedAt    time.Time `json:"issuedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Metadata    string    `json:"metadata,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ReceiptVerifyRequest is the body for POST /v1/receipts/verify.
type ReceiptVerifyRequest struct {
	ReceiptID string `json:"receiptId"`
}

// ReceiptVerifyResponse is the result of receipt verification.
type ReceiptVerifyResponse struct {
	Valid     bool   `json:"valid"`
	ReceiptID string `json:"receiptId"`
	Expired   bool   `json:"expired,omitempty"`
	Error     string `json:"error,omitempty"`
}

// listReceiptsResponse wraps GET /v1/agents/:addr/receipts.
type listReceiptsResponse struct {
	Receipts []Receipt `json:"receipts"`
}

// --- Spend Policies ---

// PolicyRule is a single constraint within a spend policy.
type PolicyRule struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// SpendPolicy defines a set of spend rules for a tenant.
type SpendPolicy struct {
	ID              string       `json:"id"`
	TenantID        string       `json:"tenantId"`
	Name            string       `json:"name"`
	Rules           []PolicyRule `json:"rules"`
	Priority        int          `json:"priority"`
	Enabled         bool         `json:"enabled"`
	EnforcementMode string       `json:"enforcementMode"`
	ShadowExpiresAt time.Time    `json:"shadowExpiresAt,omitempty"`
	CreatedAt       time.Time    `json:"createdAt"`
	UpdatedAt       time.Time    `json:"updatedAt"`
}

// CreatePolicyRequest is the body for POST /v1/agents/:addr/policies.
type CreatePolicyRequest struct {
	Name            string       `json:"name"`
	Rules           []PolicyRule `json:"rules"`
	Priority        int          `json:"priority,omitempty"`
	Enabled         *bool        `json:"enabled,omitempty"`
	EnforcementMode string       `json:"enforcementMode,omitempty"`
}

// UpdatePolicyRequest is the body for PUT /v1/agents/:addr/policies/:id.
type UpdatePolicyRequest struct {
	Name            string       `json:"name,omitempty"`
	Rules           []PolicyRule `json:"rules,omitempty"`
	Priority        *int         `json:"priority,omitempty"`
	Enabled         *bool        `json:"enabled,omitempty"`
	EnforcementMode string       `json:"enforcementMode,omitempty"`
}

// listPoliciesResponse wraps GET /v1/tenants/:id/policies.
type listPoliciesResponse struct {
	Policies []SpendPolicy `json:"policies"`
}

// --- Auth / API Keys ---

// APIKeyInfo represents metadata about an API key (secret not included).
type APIKeyInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	LastUsed  time.Time `json:"lastUsed,omitempty"`
	Revoked   bool      `json:"revoked"`
}

// AuthMe is the response from GET /v1/auth/me.
type AuthMe struct {
	AgentAddress string    `json:"agentAddress"`
	KeyID        string    `json:"keyId"`
	KeyName      string    `json:"keyName"`
	CreatedAt    time.Time `json:"createdAt"`
	LastUsed     time.Time `json:"lastUsed,omitempty"`
}

// AuthInfo is the response from GET /v1/auth/info.
type AuthInfo struct {
	Type               string   `json:"type"`
	Header             string   `json:"header"`
	AltHeader          string   `json:"altHeader"`
	Note               string   `json:"note"`
	PublicEndpoints    []string `json:"publicEndpoints"`
	ProtectedEndpoints []string `json:"protectedEndpoints"`
}

// CreateAPIKeyRequest is the body for POST /v1/auth/keys.
type CreateAPIKeyRequest struct {
	Name string `json:"name"`
}

// CreateAPIKeyResponse is the response from POST /v1/auth/keys.
type CreateAPIKeyResponse struct {
	APIKey  string `json:"apiKey"`
	KeyID   string `json:"keyId"`
	Name    string `json:"name"`
	Warning string `json:"warning"`
}

// RegenerateKeyResponse is the response from POST /v1/auth/keys/:keyId/regenerate.
type RegenerateKeyResponse struct {
	APIKey   string `json:"apiKey"`
	KeyID    string `json:"keyId"`
	OldKeyID string `json:"oldKeyId"`
	Warning  string `json:"warning"`
}

// listKeysResponse wraps GET /v1/auth/keys.
type listKeysResponse struct {
	Keys  []APIKeyInfo `json:"keys"`
	Count int          `json:"count"`
}

// --- Tenants ---

// TenantSettings holds tenant-level configuration.
type TenantSettings struct {
	RateLimitRpm     int      `json:"rateLimitRpm"`
	MaxAgents        int      `json:"maxAgents"`
	MaxSessionBudget string   `json:"maxSessionBudget"`
	AllowedOrigins   []string `json:"allowedOrigins,omitempty"`
	TakeRateBps      int      `json:"takeRateBps"`
}

// Tenant represents a multi-tenant account.
type Tenant struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	Slug                 string         `json:"slug"`
	Plan                 string         `json:"plan"`
	StripeCustomerID     string         `json:"stripeCustomerId,omitempty"`
	StripeSubscriptionID string         `json:"stripeSubscriptionId,omitempty"`
	Status               string         `json:"status"`
	Settings             TenantSettings `json:"settings"`
	CreatedAt            time.Time      `json:"createdAt"`
	UpdatedAt            time.Time      `json:"updatedAt"`
}

// CreateTenantRequest is the body for POST /v1/tenants (admin).
type CreateTenantRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	Plan string `json:"plan,omitempty"`
}

// CreateTenantResponse wraps the response from POST /v1/tenants.
type CreateTenantResponse struct {
	Tenant  Tenant `json:"tenant"`
	APIKey  string `json:"apiKey"`
	KeyID   string `json:"keyId"`
	Warning string `json:"warning"`
}

// UpdateTenantRequest is the body for PATCH /v1/tenants/:id.
type UpdateTenantRequest struct {
	Name     string          `json:"name,omitempty"`
	Plan     string          `json:"plan,omitempty"`
	Settings *TenantSettings `json:"settings,omitempty"`
}

// tenantResponse wraps GET/PATCH /v1/tenants/:id.
type tenantResponse struct {
	Tenant Tenant `json:"tenant"`
}

// TenantAgentRequest is the body for POST /v1/tenants/:id/agents.
type TenantAgentRequest struct {
	Address     string         `json:"address"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// TenantAgentResponse wraps POST /v1/tenants/:id/agents.
type TenantAgentResponse struct {
	Agent   map[string]any `json:"agent"`
	APIKey  string         `json:"apiKey"`
	KeyID   string         `json:"keyId"`
	Warning string         `json:"warning"`
}

// listTenantAgentsResponse wraps GET /v1/tenants/:id/agents.
type listTenantAgentsResponse struct {
	Agents []string `json:"agents"`
	Count  int      `json:"count"`
}

// TenantKeyRequest is the body for POST /v1/tenants/:id/keys.
type TenantKeyRequest struct {
	AgentAddr string `json:"agentAddr"`
	Name      string `json:"name"`
}

// TenantKeyInfo represents a tenant-scoped API key.
type TenantKeyInfo struct {
	ID        string    `json:"id"`
	AgentAddr string    `json:"agentAddr"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	LastUsed  time.Time `json:"lastUsed,omitempty"`
	Revoked   bool      `json:"revoked"`
}

// listTenantKeysResponse wraps GET /v1/tenants/:id/keys.
type listTenantKeysResponse struct {
	Keys  []TenantKeyInfo `json:"keys"`
	Count int             `json:"count"`
}

// TenantBilling holds billing summary for a tenant.
type TenantBilling struct {
	TotalRequests   int    `json:"totalRequests"`
	SettledRequests int    `json:"settledRequests"`
	SettledVolume   string `json:"settledVolume"`
	FeesCollected   string `json:"feesCollected"`
	TakeRateBps     int    `json:"takeRateBps"`
	Plan            string `json:"plan"`
}

// tenantBillingResponse wraps GET /v1/tenants/:id/billing.
type tenantBillingResponse struct {
	Billing TenantBilling `json:"billing"`
}

// --- Dashboard Analytics ---

// DashboardOverview is the response from GET /v1/tenants/:id/dashboard/overview.
type DashboardOverview struct {
	Tenant         map[string]any `json:"tenant"`
	Billing        TenantBilling  `json:"billing"`
	ActiveSessions int            `json:"activeSessions"`
	AgentCount     int            `json:"agentCount"`
}

// UsagePoint is a single data point in the usage time-series.
type UsagePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Requests  int       `json:"requests"`
	Volume    string    `json:"volume"`
}

// DashboardUsage is the response from GET /v1/tenants/:id/dashboard/usage.
type DashboardUsage struct {
	Interval string       `json:"interval"`
	From     time.Time    `json:"from"`
	To       time.Time    `json:"to"`
	Points   []UsagePoint `json:"points"`
	Count    int          `json:"count"`
}

// TopService represents a service ranked by volume/usage.
type TopService struct {
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName"`
	ServiceType string `json:"serviceType"`
	Requests    int    `json:"requests"`
	Volume      string `json:"volume"`
}

// DashboardDenial represents a policy denial log entry.
type DashboardDenial struct {
	SessionID   string    `json:"sessionId"`
	ServiceType string    `json:"serviceType"`
	PolicyID    string    `json:"policyId"`
	PolicyName  string    `json:"policyName"`
	Rule        string    `json:"rule"`
	Reason      string    `json:"reason"`
	Shadow      bool      `json:"shadow"`
	CreatedAt   time.Time `json:"createdAt"`
}

// --- Feed ---

// FeedEntry represents a single entry in the public timeline feed.
type FeedEntry struct {
	ID          string    `json:"id"`
	FromName    string    `json:"fromName"`
	FromAddress string    `json:"fromAddress"`
	ToName      string    `json:"toName"`
	ToAddress   string    `json:"toAddress"`
	Amount      string    `json:"amount"`
	ServiceName string    `json:"serviceName"`
	ServiceType string    `json:"serviceType"`
	TxHash      string    `json:"txHash"`
	Timestamp   time.Time `json:"timestamp"`
	TimeAgo     string    `json:"timeAgo"`
}

// FeedStats holds aggregate stats returned with the feed.
type FeedStats struct {
	TotalAgents       int    `json:"totalAgents"`
	TotalTransactions int    `json:"totalTransactions"`
	TotalVolume       string `json:"totalVolume"`
}

// FeedResponse is the response from GET /v1/feed.
type FeedResponse struct {
	Feed    []FeedEntry `json:"feed"`
	Stats   FeedStats   `json:"stats"`
	Message string      `json:"message"`
}

// --- Billing / Subscriptions ---

// SubscribeRequest is the body for POST /v1/tenants/:id/billing/subscribe.
type SubscribeRequest struct {
	Plan          string `json:"plan"`
	PaymentMethod string `json:"paymentMethod,omitempty"`
	SuccessURL    string `json:"successUrl,omitempty"`
	CancelURL     string `json:"cancelUrl,omitempty"`
}

// SubscriptionInfo represents a billing subscription.
type SubscriptionInfo struct {
	ID                string `json:"id"`
	Plan              string `json:"plan"`
	Status            string `json:"status"`
	CurrentPeriodEnd  string `json:"currentPeriodEnd,omitempty"`
	CancelAtPeriodEnd bool   `json:"cancelAtPeriodEnd"`
}

// --- Admin ---

// StuckSession represents a gateway session stuck in settlement_failed status.
type StuckSession struct {
	ID         string    `json:"id"`
	AgentAddr  string    `json:"agentAddr"`
	TenantID   string    `json:"tenantId,omitempty"`
	MaxTotal   string    `json:"maxTotal"`
	TotalSpent string    `json:"totalSpent"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expiresAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// listStuckSessionsResponse wraps GET /v1/admin/gateway/stuck.
type listStuckSessionsResponse struct {
	Sessions []StuckSession `json:"sessions"`
	Count    int            `json:"count"`
}

// ResolveResult is the response from force-resolving a stuck session.
type ResolveResult struct {
	Resolved  bool   `json:"resolved"`
	SessionID string `json:"sessionId"`
}

// RetryResult is the response from retrying a failed settlement.
type RetryResult struct {
	Retried   bool   `json:"retried"`
	SessionID string `json:"sessionId"`
}

// ForceCloseResult is the response from a force-close operation.
type ForceCloseResult struct {
	ClosedCount int `json:"closedCount"`
}

// ReconciliationReport summarizes a cross-subsystem reconciliation run.
type ReconciliationReport struct {
	LedgerMismatches int       `json:"ledgerMismatches"`
	StuckEscrows     int       `json:"stuckEscrows"`
	StaleStreams     int       `json:"staleStreams"`
	OrphanedHolds    int       `json:"orphanedHolds"`
	Healthy          bool      `json:"healthy"`
	DurationMs       int64     `json:"durationMs"`
	Timestamp        time.Time `json:"timestamp"`
}

// reconcileResponse wraps POST /v1/admin/reconcile.
type reconcileResponse struct {
	Report ReconciliationReport `json:"report"`
}

// DenialExportRecord is a denial record for ML training data export.
type DenialExportRecord struct {
	ID              int64     `json:"id"`
	AgentAddr       string    `json:"agentAddr"`
	RuleName        string    `json:"ruleName"`
	Reason          string    `json:"reason"`
	Amount          string    `json:"amount"`
	OpType          string    `json:"opType"`
	Tier            string    `json:"tier"`
	Counterparty    string    `json:"counterparty"`
	HourlyTotal     string    `json:"hourlyTotal"`
	BaselineMean    string    `json:"baselineMean"`
	BaselineStddev  string    `json:"baselineStddev"`
	OverrideAllowed bool      `json:"overrideAllowed"`
	CreatedAt       time.Time `json:"createdAt"`
}

// DenialExport is the response from GET /v1/admin/denials/export.
type DenialExport struct {
	Denials []DenialExportRecord `json:"denials"`
	Count   int                  `json:"count"`
	Since   time.Time            `json:"since"`
}

// StateInspection is the response from GET /v1/admin/state.
type StateInspection struct {
	State     map[string]any `json:"state"`
	Timestamp time.Time      `json:"timestamp"`
}

// --- Real-time Events ---

// EventType identifies a real-time event category.
type EventType string

// Event type constants for WebSocket subscriptions.
const (
	EventTransaction EventType = "transaction"
	EventAgentJoined EventType = "agent_joined"
	EventMilestone   EventType = "milestone"
	EventPriceAlert  EventType = "price_alert"
)

// RealtimeEvent represents a real-time event received over WebSocket.
type RealtimeEvent struct {
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

// RealtimeSubscription configures which events a client receives.
type RealtimeSubscription struct {
	AllEvents    bool        `json:"allEvents"`
	EventTypes   []EventType `json:"eventTypes,omitempty"`
	AgentAddrs   []string    `json:"agentAddrs,omitempty"`
	ServiceTypes []string    `json:"serviceTypes,omitempty"`
	MinAmount    float64     `json:"minAmount,omitempty"`
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
