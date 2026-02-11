// Package traces provides OpenTelemetry distributed tracing for the Alancoin platform.
package traces

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/mbd888/alancoin"

// Init initializes the OpenTelemetry tracer provider.
// If otlpEndpoint is empty, a no-op provider is used.
// Returns a shutdown function that should be called on server stop.
func Init(ctx context.Context, otlpEndpoint string, logger *slog.Logger) (func(context.Context) error, error) {
	if otlpEndpoint == "" {
		// No-op: tracing disabled
		logger.Info("tracing disabled (no OTEL_EXPORTER_OTLP_ENDPOINT set)")
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("alancoin"),
			semconv.ServiceVersion("0.1.0"),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	logger.Info("tracing enabled", "endpoint", otlpEndpoint)
	return tp.Shutdown, nil
}

// StartSpan starts a new span with the given name and returns the updated context and span.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, name)
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span
}

// Common attribute helpers for consistent span decoration.

func AgentAddr(addr string) attribute.KeyValue {
	return attribute.String("agent.addr", addr)
}

func Amount(amount string) attribute.KeyValue {
	return attribute.String("amount", amount)
}

func Reference(ref string) attribute.KeyValue {
	return attribute.String("reference", ref)
}

func EscrowID(id string) attribute.KeyValue {
	return attribute.String("escrow.id", id)
}

func StakeID(id string) attribute.KeyValue {
	return attribute.String("stake.id", id)
}

func SessionKeyID(id string) attribute.KeyValue {
	return attribute.String("session_key.id", id)
}
