package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// PipelineRequest is the HTTP payload for a multi-step proxy pipeline.
type PipelineRequest struct {
	Steps []PipelineStep `json:"steps" binding:"required,min=1,max=10"`
}

// PipelineStep defines a single step in a pipeline.
type PipelineStep struct {
	ServiceType string                 `json:"serviceType" binding:"required"`
	Params      map[string]interface{} `json:"params"`
	MaxPrice    string                 `json:"maxPrice,omitempty"`
}

// PipelineResult holds the results of all pipeline steps.
type PipelineResult struct {
	Steps      []PipelineStepResult `json:"steps"`
	TotalPaid  string               `json:"totalPaid"`
	TotalSpent string               `json:"totalSpent"`
	Remaining  string               `json:"remaining"`
}

// PipelineStepResult is the result of a single pipeline step.
type PipelineStepResult struct {
	StepIndex   int                    `json:"stepIndex"`
	ServiceType string                 `json:"serviceType"`
	Response    map[string]interface{} `json:"response"`
	ServiceUsed string                 `json:"serviceUsed"`
	ServiceName string                 `json:"serviceName"`
	AmountPaid  string                 `json:"amountPaid"`
	LatencyMs   int64                  `json:"latencyMs"`
}

// Pipeline executes a multi-step proxy pipeline within a single session.
// Each step's output is available to subsequent steps via $prev substitution.
// If any step fails, the pipeline stops and returns partial results.
func (s *Service) Pipeline(ctx context.Context, sessionID string, req PipelineRequest) (*PipelineResult, error) {
	ctx, span := traces.StartSpan(ctx, "gateway.Pipeline",
		attribute.String("session_id", sessionID),
		attribute.Int("step_count", len(req.Steps)),
	)
	defer span.End()

	if len(req.Steps) == 0 {
		return nil, fmt.Errorf("pipeline requires at least one step")
	}
	if len(req.Steps) > 10 {
		return nil, fmt.Errorf("pipeline supports at most 10 steps")
	}

	var results []PipelineStepResult
	var prevResponse map[string]interface{}

	for i, step := range req.Steps {
		// Substitute $prev references in params
		params := substitutePrev(step.Params, prevResponse)

		proxyReq := ProxyRequest{
			ServiceType: step.ServiceType,
			Params:      params,
			MaxPrice:    step.MaxPrice,
		}

		result, err := s.Proxy(ctx, sessionID, proxyReq)
		if err != nil {
			span.SetStatus(codes.Error, fmt.Sprintf("step %d failed", i))
			// Return partial results up to the failed step
			session, _ := s.GetSession(ctx, sessionID)
			totalSpent := "0.000000"
			remaining := "0.000000"
			if session != nil {
				totalSpent = session.TotalSpent
				remaining = session.Remaining()
			}
			return &PipelineResult{
				Steps:      results,
				TotalPaid:  sumStepAmounts(results),
				TotalSpent: totalSpent,
				Remaining:  remaining,
			}, fmt.Errorf("pipeline step %d (%s) failed: %w", i, step.ServiceType, err)
		}

		stepResult := PipelineStepResult{
			StepIndex:   i,
			ServiceType: step.ServiceType,
			Response:    result.Response,
			ServiceUsed: result.ServiceUsed,
			ServiceName: result.ServiceName,
			AmountPaid:  result.AmountPaid,
			LatencyMs:   result.LatencyMs,
		}
		results = append(results, stepResult)
		prevResponse = result.Response
	}

	// Get final session state for totals
	session, _ := s.GetSession(ctx, sessionID)
	totalSpent := "0.000000"
	remaining := "0.000000"
	if session != nil {
		totalSpent = session.TotalSpent
		remaining = session.Remaining()
	}

	span.SetStatus(codes.Ok, "pipeline complete")
	return &PipelineResult{
		Steps:      results,
		TotalPaid:  sumStepAmounts(results),
		TotalSpent: totalSpent,
		Remaining:  remaining,
	}, nil
}

// substitutePrev replaces $prev references in params with values from the
// previous step's response. Supports:
//   - "$prev"           → entire previous response
//   - "$prev.key"       → a specific top-level key from previous response
//   - "$prev.key.sub"   → nested key access
func substitutePrev(params map[string]interface{}, prev map[string]interface{}) map[string]interface{} {
	if params == nil || prev == nil {
		return params
	}

	result := make(map[string]interface{}, len(params))
	for k, v := range params {
		result[k] = substituteValue(v, prev)
	}
	return result
}

func substituteValue(v interface{}, prev map[string]interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return substituteString(val, prev)
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, inner := range val {
			result[k] = substituteValue(inner, prev)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, inner := range val {
			result[i] = substituteValue(inner, prev)
		}
		return result
	default:
		return v
	}
}

func substituteString(s string, prev map[string]interface{}) interface{} {
	if s == "$prev" {
		return prev
	}
	if !strings.HasPrefix(s, "$prev.") {
		return s
	}

	// Navigate nested path: "$prev.key.subkey"
	path := strings.Split(s[6:], ".") // strip "$prev."
	var current interface{} = prev
	for _, key := range path {
		m, ok := current.(map[string]interface{})
		if !ok {
			return s // path not found, return original string
		}
		current, ok = m[key]
		if !ok {
			return s // key not found, return original string
		}
	}

	// If the resolved value is a string, return it for concatenation friendliness.
	if str, ok := current.(string); ok {
		return str
	}

	// For complex types, JSON-encode for string embedding.
	b, err := json.Marshal(current)
	if err != nil {
		return s
	}
	return string(b)
}

// sumStepAmounts computes the total amount paid across all pipeline steps.
func sumStepAmounts(results []PipelineStepResult) string {
	total := new(big.Int)
	for _, r := range results {
		amt, _ := usdc.Parse(r.AmountPaid)
		if amt != nil {
			total.Add(total, amt)
		}
	}
	return usdc.Format(total)
}
