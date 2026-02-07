package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestNew_DefaultLevel(t *testing.T) {
	logger := New("", "text")
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
}

func TestNew_DebugLevel(t *testing.T) {
	logger := New("debug", "text")
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Expected debug level to be enabled")
	}
}

func TestNew_ErrorLevel(t *testing.T) {
	logger := New("error", "text")
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	if logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Expected info level to be disabled at error level")
	}
}

func TestNew_JSONFormat(t *testing.T) {
	logger := New("info", "json")
	if logger == nil {
		t.Fatal("Expected non-nil logger for JSON format")
	}
}

func TestWithRequestID_And_RequestID(t *testing.T) {
	ctx := context.Background()

	// No request ID initially
	if id := RequestID(ctx); id != "" {
		t.Errorf("Expected empty request ID, got %q", id)
	}

	// Set request ID
	ctx = WithRequestID(ctx, "req-123")
	if id := RequestID(ctx); id != "req-123" {
		t.Errorf("Expected req-123, got %q", id)
	}
}

func TestWithLogger_And_FromContext(t *testing.T) {
	ctx := context.Background()

	// Default logger when none set
	logger := FromContext(ctx)
	if logger == nil {
		t.Fatal("Expected default logger")
	}

	// Set custom logger
	custom := New("debug", "json")
	ctx = WithLogger(ctx, custom)

	retrieved := FromContext(ctx)
	if retrieved != custom {
		t.Error("Expected custom logger from context")
	}
}

func TestL_WithRequestID(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-456")
	ctx = WithLogger(ctx, New("info", "text"))

	logger := L(ctx)
	if logger == nil {
		t.Fatal("Expected non-nil logger from L()")
	}
}

func TestL_WithoutRequestID(t *testing.T) {
	ctx := context.Background()
	ctx = WithLogger(ctx, New("info", "text"))

	logger := L(ctx)
	if logger == nil {
		t.Fatal("Expected non-nil logger from L()")
	}
}

func TestRequestID_OverwritesPrevious(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "first")
	ctx = WithRequestID(ctx, "second")

	if id := RequestID(ctx); id != "second" {
		t.Errorf("Expected 'second', got %q", id)
	}
}
