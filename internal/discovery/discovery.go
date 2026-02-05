// Package discovery provides AI-powered service discovery.
//
// Instead of structured queries, agents can search naturally:
// - "Find me a cheap translator"
// - "I need fast inference under $0.01"
// - "Who has the best reputation for code review?"
//
// The AI parses intent and returns relevant matches.
package discovery

import (
	"context"
	"sort"
	"strings"
)

// SearchResult represents a matched service
type SearchResult struct {
	ServiceID    string  `json:"serviceId"`
	ServiceName  string  `json:"serviceName"`
	ServiceType  string  `json:"serviceType"`
	AgentAddress string  `json:"agentAddress"`
	AgentName    string  `json:"agentName"`
	Price        string  `json:"price"`
	PriceFloat   float64 `json:"priceFloat"`
	Reputation   float64 `json:"reputation"`
	SuccessRate  float64 `json:"successRate"`
	TxCount      int     `json:"txCount"`
	MatchScore   float64 `json:"matchScore"`  // How well it matches the query
	MatchReason  string  `json:"matchReason"` // Why this was matched
}

// ParsedQuery represents extracted intent from natural language
type ParsedQuery struct {
	ServiceTypes  []string `json:"serviceTypes"`  // Requested service types
	MaxPrice      *float64 `json:"maxPrice"`      // Price ceiling
	MinReputation *float64 `json:"minReputation"` // Minimum reputation
	SortBy        string   `json:"sortBy"`        // "price", "reputation", "speed"
	Keywords      []string `json:"keywords"`      // Additional keywords
	Intent        string   `json:"intent"`        // "find_service", "compare", "recommend"
}

// ServiceProvider fetches available services
type ServiceProvider interface {
	ListAllServices(ctx context.Context) ([]SearchResult, error)
}

// Engine provides AI-powered discovery
type Engine struct {
	services ServiceProvider
}

// NewEngine creates a new discovery engine
func NewEngine(services ServiceProvider) *Engine {
	return &Engine{services: services}
}

// Search performs natural language search
func (e *Engine) Search(ctx context.Context, query string) ([]SearchResult, *ParsedQuery, error) {
	// Parse the natural language query
	parsed := e.parseQuery(query)

	// Get all services
	services, err := e.services.ListAllServices(ctx)
	if err != nil {
		return nil, parsed, err
	}

	// Score and filter
	var results []SearchResult
	for _, svc := range services {
		score, reason := e.scoreService(svc, parsed)
		if score > 0 {
			svc.MatchScore = score
			svc.MatchReason = reason
			results = append(results, svc)
		}
	}

	// Sort by match score, then by parsed preference
	sort.Slice(results, func(i, j int) bool {
		// Primary: match score
		if results[i].MatchScore != results[j].MatchScore {
			return results[i].MatchScore > results[j].MatchScore
		}

		// Secondary: user preference
		switch parsed.SortBy {
		case "price":
			return results[i].PriceFloat < results[j].PriceFloat
		case "reputation":
			return results[i].Reputation > results[j].Reputation
		case "popular":
			return results[i].TxCount > results[j].TxCount
		default:
			// Balanced score
			scoreI := results[i].Reputation*0.4 - results[i].PriceFloat*100 + float64(results[i].TxCount)*0.1
			scoreJ := results[j].Reputation*0.4 - results[j].PriceFloat*100 + float64(results[j].TxCount)*0.1
			return scoreI > scoreJ
		}
	})

	// Limit results
	if len(results) > 20 {
		results = results[:20]
	}

	return results, parsed, nil
}

// parseQuery extracts intent from natural language
func (e *Engine) parseQuery(query string) *ParsedQuery {
	q := strings.ToLower(query)
	parsed := &ParsedQuery{
		Intent: "find_service",
	}

	// Extract service types
	serviceTypes := map[string][]string{
		"inference":   {"inference", "ai", "llm", "gpt", "model", "completion"},
		"translation": {"translation", "translate", "translator", "language"},
		"code":        {"code", "coding", "programming", "developer", "review"},
		"data":        {"data", "analysis", "analytics", "dataset"},
		"image":       {"image", "picture", "photo", "visual", "generation"},
		"audio":       {"audio", "voice", "speech", "tts", "transcription"},
		"search":      {"search", "find", "lookup", "query"},
		"storage":     {"storage", "store", "save", "backup"},
	}

	for svcType, keywords := range serviceTypes {
		for _, kw := range keywords {
			if strings.Contains(q, kw) {
				parsed.ServiceTypes = append(parsed.ServiceTypes, svcType)
				break
			}
		}
	}

	// Extract price constraints
	priceKeywords := []struct {
		words    []string
		maxPrice float64
	}{
		{[]string{"free", "no cost"}, 0},
		{[]string{"cheap", "budget", "affordable", "low cost", "inexpensive"}, 0.01},
		{[]string{"under a dollar", "less than $1"}, 1.0},
		{[]string{"under $10", "less than $10"}, 10.0},
	}

	for _, pk := range priceKeywords {
		for _, word := range pk.words {
			if strings.Contains(q, word) {
				parsed.MaxPrice = &pk.maxPrice
				parsed.SortBy = "price"
				break
			}
		}
	}

	// Extract reputation constraints
	repKeywords := []struct {
		words  []string
		minRep float64
	}{
		{[]string{"trusted", "reliable", "reputable", "established"}, 70},
		{[]string{"best", "top", "highest rated", "excellent"}, 80},
		{[]string{"elite", "premium"}, 90},
	}

	for _, rk := range repKeywords {
		for _, word := range rk.words {
			if strings.Contains(q, word) {
				parsed.MinReputation = &rk.minRep
				if parsed.SortBy == "" {
					parsed.SortBy = "reputation"
				}
				break
			}
		}
	}

	// Extract sort preference
	if strings.Contains(q, "cheapest") || strings.Contains(q, "lowest price") {
		parsed.SortBy = "price"
	} else if strings.Contains(q, "most popular") || strings.Contains(q, "most used") {
		parsed.SortBy = "popular"
	} else if strings.Contains(q, "fastest") || strings.Contains(q, "quick") {
		parsed.SortBy = "speed"
	}

	// Extract comparison intent
	if strings.Contains(q, "compare") || strings.Contains(q, "vs") || strings.Contains(q, "versus") {
		parsed.Intent = "compare"
	} else if strings.Contains(q, "recommend") || strings.Contains(q, "suggest") || strings.Contains(q, "should i") {
		parsed.Intent = "recommend"
	}

	return parsed
}

// scoreService calculates how well a service matches the query
func (e *Engine) scoreService(svc SearchResult, parsed *ParsedQuery) (float64, string) {
	score := 0.0
	reasons := []string{}

	// Service type match (most important)
	if len(parsed.ServiceTypes) > 0 {
		matched := false
		for _, st := range parsed.ServiceTypes {
			if strings.EqualFold(svc.ServiceType, st) {
				score += 50
				reasons = append(reasons, "Matches service type")
				matched = true
				break
			}
		}
		if !matched {
			return 0, "" // Must match service type if specified
		}
	} else {
		score += 20 // No type specified, include everything
	}

	// Price constraint
	if parsed.MaxPrice != nil {
		if svc.PriceFloat <= *parsed.MaxPrice {
			score += 20
			reasons = append(reasons, "Within budget")
		} else {
			return 0, "" // Over budget, exclude
		}
	}

	// Reputation constraint
	if parsed.MinReputation != nil {
		if svc.Reputation >= *parsed.MinReputation {
			score += 15
			reasons = append(reasons, "High reputation")
		} else {
			return 0, "" // Below reputation threshold, exclude
		}
	}

	// Bonus for high reputation
	if svc.Reputation >= 80 {
		score += 10
		reasons = append(reasons, "Trusted agent")
	}

	// Bonus for high success rate
	if svc.SuccessRate >= 0.95 {
		score += 5
		reasons = append(reasons, "95%+ success rate")
	}

	// Bonus for activity
	if svc.TxCount >= 100 {
		score += 5
		reasons = append(reasons, "Highly active")
	}

	reason := strings.Join(reasons, "; ")
	return score, reason
}

// Recommend provides AI-generated recommendations
func (e *Engine) Recommend(ctx context.Context, query string, topN int) ([]SearchResult, string, error) {
	results, parsed, err := e.Search(ctx, query)
	if err != nil {
		return nil, "", err
	}

	if len(results) == 0 {
		return nil, "No services found matching your criteria.", nil
	}

	if topN > 0 && len(results) > topN {
		results = results[:topN]
	}

	// Generate recommendation text
	var recommendation string
	top := results[0]

	switch parsed.Intent {
	case "recommend":
		recommendation = "Based on your needs, I recommend " + top.AgentName + "'s " +
			top.ServiceName + " ($" + top.Price + "). " + top.MatchReason + "."
	case "compare":
		recommendation = "Here are the top options to compare. " +
			top.AgentName + " offers the best overall match."
	default:
		recommendation = "Found " + string(rune('0'+len(results))) + " matching services. " +
			"Top pick: " + top.AgentName + " (" + top.ServiceType + " at $" + top.Price + ")."
	}

	return results, recommendation, nil
}
