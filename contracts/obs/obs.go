// Package obs sets up structured logging and tracing the same way in every
// service (roadmap 2.4 and 2.5, docs/observability.md).
//
//   - Logs are JSON on stdout. Every record logged with a context carries
//     trace_id and span_id, so logs join traces.
//   - Traces use OpenTelemetry. They are exported over OTLP/HTTP only when
//     OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise spans are still created
//     (so IDs appear in logs) but go nowhere.
//   - The W3C traceparent travels in HTTP headers and AMQP message headers.
package obs

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Setup configures the default slog logger and the global tracer provider.
// Call the returned function on shutdown to flush pending spans.
func Setup(ctx context.Context, service string) (func(context.Context) error, error) {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	// SetDefault also routes the standard library log package through slog.
	slog.SetDefault(slog.New(traceHandler{handler}).With("service", service))

	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", service)))
	if err != nil {
		return nil, err
	}
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio()))),
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		exporter, err := otlptracehttp.New(ctx) // reads OTEL_EXPORTER_OTLP_* variables
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second)))
	}
	provider := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return provider.Shutdown, nil
}

// sampleRatio reads OTEL_TRACES_SAMPLER_ARG (0..1); default 1 (everything).
func sampleRatio() float64 {
	ratio, err := strconv.ParseFloat(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return 1
	}
	return ratio
}

// Tracer returns the named tracer from the global provider.
func Tracer(name string) trace.Tracer { return otel.Tracer(name) }

// traceHandler adds trace_id and span_id to records logged with a context
// that carries a span.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		record.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

// AMQPCarrier adapts AMQP headers to OpenTelemetry propagation.
type AMQPCarrier amqp.Table

func (c AMQPCarrier) Get(key string) string {
	if value, ok := c[key].(string); ok {
		return value
	}
	return ""
}

func (c AMQPCarrier) Set(key, value string) { c[key] = value }

func (c AMQPCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

// InjectAMQP writes the current trace context into headers (allocating them
// when nil) and returns them.
func InjectAMQP(ctx context.Context, headers amqp.Table) amqp.Table {
	if headers == nil {
		headers = amqp.Table{}
	}
	otel.GetTextMapPropagator().Inject(ctx, AMQPCarrier(headers))
	return headers
}

// ExtractAMQP returns ctx continued from the trace context in headers.
func ExtractAMQP(ctx context.Context, headers amqp.Table) context.Context {
	if headers == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, AMQPCarrier(headers))
}
