package gateway

import (
	"testing"
)

func TestSubstitutePrev_NilPrev(t *testing.T) {
	params := map[string]interface{}{"text": "$prev.output"}
	result := substitutePrev(params, nil)
	if result["text"] != "$prev.output" {
		t.Fatalf("expected no substitution with nil prev, got %v", result["text"])
	}
}

func TestSubstitutePrev_NilParams(t *testing.T) {
	prev := map[string]interface{}{"output": "hello"}
	result := substitutePrev(nil, prev)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestSubstitutePrev_EntireResponse(t *testing.T) {
	params := map[string]interface{}{"data": "$prev"}
	prev := map[string]interface{}{"output": "hello", "score": 0.95}
	result := substitutePrev(params, prev)

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T: %v", result["data"], result["data"])
	}
	if data["output"] != "hello" {
		t.Fatalf("expected hello, got %v", data["output"])
	}
}

func TestSubstitutePrev_TopLevelKey(t *testing.T) {
	params := map[string]interface{}{"text": "$prev.output"}
	prev := map[string]interface{}{"output": "hello world"}
	result := substitutePrev(params, prev)

	if result["text"] != "hello world" {
		t.Fatalf("expected 'hello world', got %v", result["text"])
	}
}

func TestSubstitutePrev_NestedKey(t *testing.T) {
	params := map[string]interface{}{"text": "$prev.result.summary"}
	prev := map[string]interface{}{
		"result": map[string]interface{}{
			"summary": "the quick brown fox",
		},
	}
	result := substitutePrev(params, prev)

	if result["text"] != "the quick brown fox" {
		t.Fatalf("expected 'the quick brown fox', got %v", result["text"])
	}
}

func TestSubstitutePrev_MissingKey(t *testing.T) {
	params := map[string]interface{}{"text": "$prev.nonexistent"}
	prev := map[string]interface{}{"output": "hello"}
	result := substitutePrev(params, prev)

	// Should return the original string when key not found
	if result["text"] != "$prev.nonexistent" {
		t.Fatalf("expected original string, got %v", result["text"])
	}
}

func TestSubstitutePrev_NoSubstitutionNeeded(t *testing.T) {
	params := map[string]interface{}{
		"text":     "no substitution here",
		"count":    42,
		"previous": "$previous.not_a_ref",
	}
	prev := map[string]interface{}{"output": "hello"}
	result := substitutePrev(params, prev)

	if result["text"] != "no substitution here" {
		t.Fatalf("unexpected substitution in text: %v", result["text"])
	}
	if result["count"] != 42 {
		t.Fatalf("unexpected change to count: %v", result["count"])
	}
	if result["previous"] != "$previous.not_a_ref" {
		t.Fatalf("unexpected substitution in previous: %v", result["previous"])
	}
}

func TestSubstitutePrev_NestedParams(t *testing.T) {
	params := map[string]interface{}{
		"config": map[string]interface{}{
			"input": "$prev.output",
			"lang":  "en",
		},
	}
	prev := map[string]interface{}{"output": "translated text"}
	result := substitutePrev(params, prev)

	config, ok := result["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested map, got %T", result["config"])
	}
	if config["input"] != "translated text" {
		t.Fatalf("expected 'translated text', got %v", config["input"])
	}
	if config["lang"] != "en" {
		t.Fatalf("expected 'en', got %v", config["lang"])
	}
}

func TestSubstitutePrev_ArrayParams(t *testing.T) {
	params := map[string]interface{}{
		"items": []interface{}{"$prev.first", "static", "$prev.second"},
	}
	prev := map[string]interface{}{"first": "a", "second": "b"}
	result := substitutePrev(params, prev)

	items, ok := result["items"].([]interface{})
	if !ok {
		t.Fatalf("expected array, got %T", result["items"])
	}
	if items[0] != "a" || items[1] != "static" || items[2] != "b" {
		t.Fatalf("unexpected array: %v", items)
	}
}

func TestSumStepAmounts(t *testing.T) {
	results := []PipelineStepResult{
		{AmountPaid: "0.005000"},
		{AmountPaid: "0.010000"},
		{AmountPaid: "0.003000"},
	}
	total := sumStepAmounts(results)
	if total != "0.018000" {
		t.Fatalf("expected 0.018000, got %s", total)
	}
}

func TestSumStepAmounts_Empty(t *testing.T) {
	total := sumStepAmounts(nil)
	if total != "0.000000" {
		t.Fatalf("expected 0.000000, got %s", total)
	}
}

func TestSubstitutePrev_ComplexValueBecomesJSON(t *testing.T) {
	params := map[string]interface{}{"data": "$prev.nested"}
	prev := map[string]interface{}{
		"nested": map[string]interface{}{
			"key": "value",
			"num": float64(42),
		},
	}
	result := substitutePrev(params, prev)

	// Complex value should be JSON-encoded as string
	str, ok := result["data"].(string)
	if !ok {
		t.Fatalf("expected string, got %T: %v", result["data"], result["data"])
	}
	// Should contain the JSON representation
	if str == "" || str == "$prev.nested" {
		t.Fatalf("expected JSON string, got %s", str)
	}
}
