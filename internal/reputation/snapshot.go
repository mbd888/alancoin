package reputation

import "time"

// Snapshot is a point-in-time reputation score stored for history.
type Snapshot struct {
	ID             int       `json:"id"`
	Address        string    `json:"address"`
	Score          float64   `json:"score"`
	Tier           Tier      `json:"tier"`
	VolumeScore    float64   `json:"volumeScore"`
	ActivityScore  float64   `json:"activityScore"`
	SuccessScore   float64   `json:"successScore"`
	AgeScore       float64   `json:"ageScore"`
	DiversityScore float64   `json:"diversityScore"`
	TotalTxns      int       `json:"totalTransactions"`
	TotalVolume    float64   `json:"totalVolume"`
	SuccessRate    float64   `json:"successRate"`
	UniquePeers    int       `json:"uniquePeers"`
	CreatedAt      time.Time `json:"createdAt"`
}

// SnapshotFromScore creates a Snapshot from a calculated Score.
func SnapshotFromScore(s *Score) *Snapshot {
	var successRate float64
	if s.Metrics.TotalTransactions > 0 {
		successRate = float64(s.Metrics.SuccessfulTxns) / float64(s.Metrics.TotalTransactions)
	}
	return &Snapshot{
		Address:        s.Address,
		Score:          s.Score,
		Tier:           s.Tier,
		VolumeScore:    s.Components.VolumeScore,
		ActivityScore:  s.Components.ActivityScore,
		SuccessScore:   s.Components.SuccessScore,
		AgeScore:       s.Components.AgeScore,
		DiversityScore: s.Components.DiversityScore,
		TotalTxns:      s.Metrics.TotalTransactions,
		TotalVolume:    s.Metrics.TotalVolumeUSD,
		SuccessRate:    successRate,
		UniquePeers:    s.Metrics.UniqueCounterparties,
		CreatedAt:      time.Now(),
	}
}

// SignedScore wraps a Score with HMAC signature and validity window.
type SignedScore struct {
	Reputation *Score `json:"reputation"`
	Signature  string `json:"signature,omitempty"`
	IssuedAt   string `json:"issuedAt,omitempty"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
}

// BatchRequest is a request for batch reputation lookups.
type BatchRequest struct {
	Addresses []string `json:"addresses" binding:"required"`
}

// BatchResponse returns multiple reputation scores.
type BatchResponse struct {
	Scores    []*SignedScore `json:"scores"`
	Signature string         `json:"signature,omitempty"`
	IssuedAt  string         `json:"issuedAt,omitempty"`
	ExpiresAt string         `json:"expiresAt,omitempty"`
}

// CompareRequest is a request for comparing agents.
type CompareRequest struct {
	Addresses []string `json:"addresses" binding:"required"`
}

// CompareResponse returns scores side-by-side.
type CompareResponse struct {
	Agents    []*Score `json:"agents"`
	Best      string   `json:"best"`
	Signature string   `json:"signature,omitempty"`
	IssuedAt  string   `json:"issuedAt,omitempty"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
}

// HistoryQuery holds query parameters for historical scores.
type HistoryQuery struct {
	Address string
	From    time.Time
	To      time.Time
	Limit   int
}
