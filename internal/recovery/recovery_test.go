package recovery

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSafe_NoPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	called := false
	Safe(logger, "test", func() {
		called = true
	})

	if !called {
		t.Fatal("fn was not called")
	}
	if buf.Len() > 0 {
		t.Fatalf("unexpected log output: %s", buf.String())
	}
}

func TestSafe_WithPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	Safe(logger, "test_component", func() {
		panic("boom")
	})

	output := buf.String()
	if !strings.Contains(output, "recovered panic") {
		t.Fatalf("expected panic log, got: %s", output)
	}
	if !strings.Contains(output, "test_component") {
		t.Fatalf("expected component name in log, got: %s", output)
	}
	if !strings.Contains(output, "boom") {
		t.Fatalf("expected panic value in log, got: %s", output)
	}
}

func TestLogPanic_NoPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	func() {
		defer LogPanic(logger, "no_panic")
	}()

	if buf.Len() > 0 {
		t.Fatalf("unexpected log output: %s", buf.String())
	}
}

func TestLogPanic_WithPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	func() {
		defer LogPanic(logger, "panicker")
		panic("test error")
	}()

	output := buf.String()
	if !strings.Contains(output, "panicker") {
		t.Fatalf("expected component in log, got: %s", output)
	}
	if !strings.Contains(output, "test error") {
		t.Fatalf("expected panic message in log, got: %s", output)
	}
}
