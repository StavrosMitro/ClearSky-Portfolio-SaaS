package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestTraceContextTravelsThroughAMQPHeaders(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	ctx, span := provider.Tracer("test").Start(context.Background(), "publish")
	headers := InjectAMQP(ctx, nil)
	span.End()
	if headers["traceparent"] == nil {
		t.Fatalf("traceparent was not injected: %v", headers)
	}

	received := ExtractAMQP(context.Background(), amqp.Table(headers))
	_, child := provider.Tracer("test").Start(received, "consume")
	defer child.End()
	if child.SpanContext().TraceID() != span.SpanContext().TraceID() {
		t.Fatal("the consumer span must continue the publisher's trace")
	}
}

func TestLogsCarryTraceIDs(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	var buf bytes.Buffer
	logger := slog.New(traceHandler{slog.NewJSONHandler(&buf, nil)})

	ctx, span := provider.Tracer("test").Start(context.Background(), "work")
	logger.InfoContext(ctx, "processing")
	span.End()

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["trace_id"] != span.SpanContext().TraceID().String() || record["span_id"] == nil {
		t.Fatalf("log record lacks trace ids: %v", record)
	}
}
