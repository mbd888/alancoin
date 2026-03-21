package eventbus

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/mbd888/alancoin/eventbus")

// StartPublishSpan creates a trace span for event publishing.
// The span captures the topic, key, and event ID.
func StartPublishSpan(ctx context.Context, event Event) (context.Context, trace.Span) {
	return tracer.Start(ctx, "eventbus.publish",
		trace.WithAttributes(
			attribute.String("eventbus.topic", event.Topic),
			attribute.String("eventbus.key", event.Key),
			attribute.String("eventbus.event_id", event.ID),
		),
		trace.WithSpanKind(trace.SpanKindProducer),
	)
}

// StartConsumeSpan creates a trace span for batch consumption.
// Links back to the original publish spans for end-to-end tracing.
func StartConsumeSpan(ctx context.Context, consumerGroup string, batchSize int) (context.Context, trace.Span) {
	return tracer.Start(ctx, "eventbus.consume."+consumerGroup,
		trace.WithAttributes(
			attribute.String("eventbus.consumer", consumerGroup),
			attribute.Int("eventbus.batch_size", batchSize),
		),
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
}

// InjectTraceContext stores the current trace context into the event's RequestID field.
// This allows consumers to link back to the originating request's trace.
func InjectTraceContext(ctx context.Context, event *Event) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		event.RequestID = span.SpanContext().TraceID().String()
	}
}
