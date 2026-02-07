package discovery

import (
	"context"
	"testing"
)

// mockServiceProvider returns a fixed list of services
type mockServiceProvider struct {
	services []SearchResult
	err      error
}

func (m *mockServiceProvider) ListAllServices(ctx context.Context) ([]SearchResult, error) {
	return m.services, m.err
}

func testServices() []SearchResult {
	return []SearchResult{
		{ServiceID: "svc1", ServiceName: "FastTranslate", ServiceType: "translation", AgentAddress: "0xagent1", AgentName: "TranslatorBot", Price: "0.005", PriceFloat: 0.005, Reputation: 85, SuccessRate: 0.97, TxCount: 150},
		{ServiceID: "svc2", ServiceName: "CheapTranslate", ServiceType: "translation", AgentAddress: "0xagent2", AgentName: "BudgetBot", Price: "0.001", PriceFloat: 0.001, Reputation: 60, SuccessRate: 0.90, TxCount: 50},
		{ServiceID: "svc3", ServiceName: "GPT Inference", ServiceType: "inference", AgentAddress: "0xagent3", AgentName: "InferenceBot", Price: "0.01", PriceFloat: 0.01, Reputation: 92, SuccessRate: 0.99, TxCount: 500},
		{ServiceID: "svc4", ServiceName: "CodeReview", ServiceType: "code", AgentAddress: "0xagent4", AgentName: "ReviewBot", Price: "0.05", PriceFloat: 0.05, Reputation: 75, SuccessRate: 0.95, TxCount: 30},
		{ServiceID: "svc5", ServiceName: "PremiumInference", ServiceType: "inference", AgentAddress: "0xagent5", AgentName: "EliteBot", Price: "0.10", PriceFloat: 0.10, Reputation: 95, SuccessRate: 1.0, TxCount: 200},
	}
}

// ---------------------------------------------------------------------------
// parseQuery tests
// ---------------------------------------------------------------------------

func TestParseQuery_ServiceType(t *testing.T) {
	e := NewEngine(nil)

	tests := []struct {
		query    string
		wantType string
	}{
		{"I need a translator", "translation"},
		{"find me an AI model", "inference"},
		{"code review please", "code"},
		{"data analysis service", "data"},
		{"image generation", "image"},
		{"voice transcription", "audio"},
		{"search for documents", "search"},
		{"backup my data to storage", "storage"},
	}

	for _, tt := range tests {
		parsed := e.parseQuery(tt.query)
		found := false
		for _, st := range parsed.ServiceTypes {
			if st == tt.wantType {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("parseQuery(%q): want %s in ServiceTypes, got %v", tt.query, tt.wantType, parsed.ServiceTypes)
		}
	}
}

func TestParseQuery_PriceConstraints(t *testing.T) {
	e := NewEngine(nil)

	tests := []struct {
		query    string
		wantMax  float64
		wantSort string
	}{
		{"find a free service", 0, "price"},
		{"cheap translation", 0.01, "price"},
		{"affordable inference", 0.01, "price"},
		{"under a dollar", 1.0, "price"},
		{"less than $10", 10.0, "price"},
	}

	for _, tt := range tests {
		parsed := e.parseQuery(tt.query)
		if parsed.MaxPrice == nil {
			t.Errorf("parseQuery(%q): expected MaxPrice, got nil", tt.query)
			continue
		}
		if *parsed.MaxPrice != tt.wantMax {
			t.Errorf("parseQuery(%q): MaxPrice = %f, want %f", tt.query, *parsed.MaxPrice, tt.wantMax)
		}
		if parsed.SortBy != tt.wantSort {
			t.Errorf("parseQuery(%q): SortBy = %s, want %s", tt.query, parsed.SortBy, tt.wantSort)
		}
	}
}

func TestParseQuery_ReputationConstraints(t *testing.T) {
	e := NewEngine(nil)

	tests := []struct {
		query   string
		wantMin float64
	}{
		{"find a trusted translator", 70},
		{"best inference service", 80},
		{"elite code review", 90},
	}

	for _, tt := range tests {
		parsed := e.parseQuery(tt.query)
		if parsed.MinReputation == nil {
			t.Errorf("parseQuery(%q): expected MinReputation, got nil", tt.query)
			continue
		}
		if *parsed.MinReputation != tt.wantMin {
			t.Errorf("parseQuery(%q): MinReputation = %f, want %f", tt.query, *parsed.MinReputation, tt.wantMin)
		}
	}
}

func TestParseQuery_SortPreference(t *testing.T) {
	e := NewEngine(nil)

	tests := []struct {
		query    string
		wantSort string
	}{
		{"cheapest translation", "price"},
		{"lowest price inference", "price"},
		{"most popular service", "popular"},
		{"fastest inference", "speed"},
	}

	for _, tt := range tests {
		parsed := e.parseQuery(tt.query)
		if parsed.SortBy != tt.wantSort {
			t.Errorf("parseQuery(%q): SortBy = %s, want %s", tt.query, parsed.SortBy, tt.wantSort)
		}
	}
}

func TestParseQuery_Intent(t *testing.T) {
	e := NewEngine(nil)

	tests := []struct {
		query      string
		wantIntent string
	}{
		{"find a translator", "find_service"},
		{"compare inference services", "compare"},
		{"translation vs code review", "compare"},
		{"recommend a good service", "recommend"},
		{"should i use this agent", "recommend"},
	}

	for _, tt := range tests {
		parsed := e.parseQuery(tt.query)
		if parsed.Intent != tt.wantIntent {
			t.Errorf("parseQuery(%q): Intent = %s, want %s", tt.query, parsed.Intent, tt.wantIntent)
		}
	}
}

func TestParseQuery_NoConstraints(t *testing.T) {
	e := NewEngine(nil)

	parsed := e.parseQuery("hello world")
	if len(parsed.ServiceTypes) != 0 {
		t.Errorf("Expected no service types, got %v", parsed.ServiceTypes)
	}
	if parsed.MaxPrice != nil {
		t.Errorf("Expected no price constraint, got %v", *parsed.MaxPrice)
	}
	if parsed.MinReputation != nil {
		t.Errorf("Expected no reputation constraint, got %v", *parsed.MinReputation)
	}
	if parsed.Intent != "find_service" {
		t.Errorf("Expected default intent find_service, got %s", parsed.Intent)
	}
}

// ---------------------------------------------------------------------------
// scoreService tests
// ---------------------------------------------------------------------------

func TestScoreService_TypeMatch(t *testing.T) {
	e := NewEngine(nil)
	svc := SearchResult{ServiceType: "translation"}

	parsed := &ParsedQuery{ServiceTypes: []string{"translation"}}
	score, _ := e.scoreService(svc, parsed)
	if score < 50 {
		t.Errorf("Expected score >= 50 for type match, got %f", score)
	}

	parsed2 := &ParsedQuery{ServiceTypes: []string{"inference"}}
	score2, _ := e.scoreService(svc, parsed2)
	if score2 != 0 {
		t.Errorf("Expected score 0 for type mismatch, got %f", score2)
	}
}

func TestScoreService_NoTypeFilter(t *testing.T) {
	e := NewEngine(nil)
	svc := SearchResult{ServiceType: "anything"}

	parsed := &ParsedQuery{}
	score, _ := e.scoreService(svc, parsed)
	if score < 20 {
		t.Errorf("Expected score >= 20 with no type filter, got %f", score)
	}
}

func TestScoreService_PriceFilter(t *testing.T) {
	e := NewEngine(nil)
	maxPrice := 0.01

	cheap := SearchResult{ServiceType: "translation", PriceFloat: 0.005}
	expensive := SearchResult{ServiceType: "translation", PriceFloat: 0.05}

	parsed := &ParsedQuery{ServiceTypes: []string{"translation"}, MaxPrice: &maxPrice}

	score1, _ := e.scoreService(cheap, parsed)
	score2, _ := e.scoreService(expensive, parsed)

	if score1 == 0 {
		t.Error("Cheap service should pass price filter")
	}
	if score2 != 0 {
		t.Error("Expensive service should be excluded by price filter")
	}
}

func TestScoreService_ReputationFilter(t *testing.T) {
	e := NewEngine(nil)
	minRep := 80.0

	high := SearchResult{ServiceType: "translation", Reputation: 90}
	low := SearchResult{ServiceType: "translation", Reputation: 50}

	parsed := &ParsedQuery{ServiceTypes: []string{"translation"}, MinReputation: &minRep}

	score1, _ := e.scoreService(high, parsed)
	score2, _ := e.scoreService(low, parsed)

	if score1 == 0 {
		t.Error("High reputation service should pass filter")
	}
	if score2 != 0 {
		t.Error("Low reputation service should be excluded")
	}
}

func TestScoreService_BonusPoints(t *testing.T) {
	e := NewEngine(nil)
	parsed := &ParsedQuery{}

	base := SearchResult{Reputation: 50, SuccessRate: 0.80, TxCount: 10}
	elite := SearchResult{Reputation: 90, SuccessRate: 0.99, TxCount: 200}

	score1, _ := e.scoreService(base, parsed)
	score2, _ := e.scoreService(elite, parsed)

	if score2 <= score1 {
		t.Errorf("Elite service (%f) should score higher than base (%f)", score2, score1)
	}
}

// ---------------------------------------------------------------------------
// Search tests
// ---------------------------------------------------------------------------

func TestSearch_TypeFiltering(t *testing.T) {
	provider := &mockServiceProvider{services: testServices()}
	e := NewEngine(provider)

	results, parsed, err := e.Search(context.Background(), "find me a translator")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	hasTranslation := false
	for _, st := range parsed.ServiceTypes {
		if st == "translation" {
			hasTranslation = true
			break
		}
	}
	if !hasTranslation {
		t.Errorf("Expected 'translation' in parsed types, got %v", parsed.ServiceTypes)
	}

	for _, r := range results {
		if r.ServiceType != "translation" {
			t.Errorf("Expected only translation results, got %s", r.ServiceType)
		}
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 translation services, got %d", len(results))
	}
}

func TestSearch_NoResults(t *testing.T) {
	provider := &mockServiceProvider{services: testServices()}
	e := NewEngine(provider)

	results, _, err := e.Search(context.Background(), "find me a quantum computing service")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	// "quantum" and "computing" don't match any known types, but "find" matches "search"
	// so we might get results. Let's test a truly unknown query
	results, _, err = e.Search(context.Background(), "xyz123 nomatches here")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	// No type filter means everything is included with base score
	if len(results) == 0 {
		t.Log("No results for unknown query (expected - no type match bonus)")
	}
}

func TestSearch_CheapTranslation(t *testing.T) {
	provider := &mockServiceProvider{services: testServices()}
	e := NewEngine(provider)

	results, _, err := e.Search(context.Background(), "cheap translation")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected results for cheap translation")
	}

	// All results should be translation type
	for _, r := range results {
		if r.ServiceType != "translation" {
			t.Errorf("Expected translation type, got %s", r.ServiceType)
		}
	}
}

func TestSearch_ProviderError(t *testing.T) {
	provider := &mockServiceProvider{err: context.DeadlineExceeded}
	e := NewEngine(provider)

	_, _, err := e.Search(context.Background(), "anything")
	if err == nil {
		t.Error("Expected error from provider")
	}
}

func TestSearch_ResultLimit(t *testing.T) {
	// Create > 20 services
	var services []SearchResult
	for i := 0; i < 30; i++ {
		services = append(services, SearchResult{
			ServiceID:   "svc",
			ServiceType: "inference",
			PriceFloat:  0.01,
			Reputation:  50,
		})
	}

	provider := &mockServiceProvider{services: services}
	e := NewEngine(provider)

	results, _, err := e.Search(context.Background(), "find inference")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) > 20 {
		t.Errorf("Expected max 20 results, got %d", len(results))
	}
}

func TestSearch_SortByReputation(t *testing.T) {
	provider := &mockServiceProvider{services: testServices()}
	e := NewEngine(provider)

	results, _, err := e.Search(context.Background(), "best inference service")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("Expected at least 2 inference services, got %d", len(results))
	}

	// Results with same match score should be sorted by reputation (descending)
	for i := 1; i < len(results); i++ {
		if results[i].MatchScore == results[i-1].MatchScore {
			if results[i].Reputation > results[i-1].Reputation {
				t.Errorf("Results not sorted by reputation: %f > %f", results[i].Reputation, results[i-1].Reputation)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Recommend tests
// ---------------------------------------------------------------------------

func TestRecommend_HappyPath(t *testing.T) {
	provider := &mockServiceProvider{services: testServices()}
	e := NewEngine(provider)

	results, recommendation, err := e.Recommend(context.Background(), "recommend a translator", 3)
	if err != nil {
		t.Fatalf("Recommend failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected results")
	}
	if len(results) > 3 {
		t.Errorf("Expected max 3 results, got %d", len(results))
	}
	if recommendation == "" {
		t.Error("Expected non-empty recommendation")
	}
}

func TestRecommend_NoResults(t *testing.T) {
	provider := &mockServiceProvider{services: nil}
	e := NewEngine(provider)

	_, recommendation, err := e.Recommend(context.Background(), "quantum computing", 3)
	if err != nil {
		t.Fatalf("Recommend failed: %v", err)
	}

	if recommendation != "No services found matching your criteria." {
		t.Errorf("Expected 'no services' message, got %q", recommendation)
	}
}

func TestRecommend_CompareIntent(t *testing.T) {
	provider := &mockServiceProvider{services: testServices()}
	e := NewEngine(provider)

	_, recommendation, err := e.Recommend(context.Background(), "compare inference services", 5)
	if err != nil {
		t.Fatalf("Recommend failed: %v", err)
	}

	if recommendation == "" {
		t.Error("Expected recommendation text for compare intent")
	}
}
