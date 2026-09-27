package amqpx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"clearsky/contracts/obs"
	"clearsky/contracts/rpc"
	"clearsky/contracts/topology"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// These tests need a real broker: TEST_AMQP_URL (skipped when unset).
func dialTest(t *testing.T) *amqp.Connection {
	t.Helper()
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL is not set; skipping RabbitMQ test")
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func testQueue() topology.Queue {
	id := uuid.NewString()[:8]
	return topology.Queue{Name: "test.q." + id, Keys: []string{"test.key." + id}}
}

// call publishes an RPC request and waits for the reply.
func call(t *testing.T, conn *amqp.Connection, ctx context.Context, key string, body any) []byte {
	t.Helper()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	reply, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := ch.Consume(reply.Name, "", true, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	corr := uuid.NewString()
	if err := ch.PublishWithContext(ctx, topology.CommandsExchange, key, true, false, amqp.Publishing{
		CorrelationId: corr, ReplyTo: reply.Name, Body: raw, Headers: obs.InjectAMQP(ctx, nil),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-msgs:
		if d.CorrelationId != corr {
			t.Fatalf("correlation id %q, want %q", d.CorrelationId, corr)
		}
		return d.Body
	case <-time.After(10 * time.Second):
		t.Fatal("no reply")
	}
	return nil
}

func TestServerRepliesWithEnvelopeAndContinuesTrace(t *testing.T) {
	conn := dialTest(t)
	provider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	q := testQueue()
	seenTrace := make(chan trace.TraceID, 1)
	server := NewServer("test", q, 2, func(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
		seenTrace <- trace.SpanContextFromContext(ctx).TraceID()
		switch msgType {
		case "echo":
			var in struct{ Value string }
			_ = json.Unmarshal(body, &in)
			return map[string]string{"value": in.Value}, nil
		case "conflict":
			return nil, rpc.Fail(rpc.CodeConflict, "Already exists")
		case "boom":
			return nil, errors.New("database password is hunter2")
		default:
			panic("unexpected type")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx, conn) }()
	time.Sleep(500 * time.Millisecond)

	traceCtx, span := provider.Tracer("test").Start(context.Background(), "caller")
	defer span.End()
	var out struct{ Value string }
	if err := rpc.Decode(call(t, conn, traceCtx, q.Keys[0], map[string]string{"type": "echo", "value": "hi"}), &out); err != nil || out.Value != "hi" {
		t.Fatalf("echo: %+v, %v", out, err)
	}
	if got := <-seenTrace; got != span.SpanContext().TraceID() {
		t.Fatal("the handler must run inside the caller's trace")
	}

	var remote *rpc.RemoteError
	if err := rpc.Decode(call(t, conn, context.Background(), q.Keys[0], map[string]string{"type": "conflict"}), nil); !errors.As(err, &remote) || remote.RPCError.Code != rpc.CodeConflict {
		t.Fatalf("conflict: %v", err)
	}
	<-seenTrace
	err := rpc.Decode(call(t, conn, context.Background(), q.Keys[0], map[string]string{"type": "boom"}), nil)
	if !errors.As(err, &remote) || remote.RPCError.Code != rpc.CodeInternal || remote.RPCError.Message == "database password is hunter2" {
		t.Fatalf("internal errors must be generic: %v", err)
	}
	<-seenTrace
	if err := rpc.Decode(call(t, conn, context.Background(), q.Keys[0], map[string]string{"type": "panic"}), nil); !errors.As(err, &remote) || remote.RPCError.Code != rpc.CodeInternal {
		t.Fatalf("a panic must become an internal error: %v", err)
	}
}

func TestPublisherDeliversAndDetectsUnroutable(t *testing.T) {
	conn := dialTest(t)
	q := testQueue()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if err := topology.Declare(ch, q); err != nil {
		t.Fatal(err)
	}
	pub, err := NewPublisher(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pub.Publish(ctx, q.Keys[0], []byte(`{"type":"x"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msg, ok, err := ch.Get(q.Name, true)
	if err != nil || !ok || string(msg.Body) != `{"type":"x"}` || msg.DeliveryMode != amqp.Persistent {
		t.Fatalf("stored message: ok=%v err=%v body=%s", ok, err, msg.Body)
	}
	if err := pub.Publish(ctx, "no.such.key."+uuid.NewString(), []byte(`{}`)); !errors.Is(err, ErrUnroutable) {
		t.Fatalf("unroutable publish: %v", err)
	}
}

func TestFailedFireAndForgetMessageIsDeadLettered(t *testing.T) {
	conn := dialTest(t)
	q := testQueue()
	server := NewServer("test", q, 1, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "bad message")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx, conn) }()
	time.Sleep(500 * time.Millisecond)

	pub, err := NewPublisher(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	if err := pub.Publish(context.Background(), q.Keys[0], []byte(`{"type":"bad"}`)); err != nil {
		t.Fatal(err)
	}
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok, _ := ch.Get(q.Name+".dlq", true); ok {
			if string(msg.Body) != `{"type":"bad"}` {
				t.Fatalf("dead-lettered body = %s", msg.Body)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the rejected message did not reach the DLQ")
}

func TestClientCallsAServer(t *testing.T) {
	conn := dialTest(t)
	q := testQueue()
	server := NewServer("test", q, 1, func(_ context.Context, msgType string, _ json.RawMessage) (any, error) {
		return map[string]string{"echo": msgType}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx, conn) }()
	time.Sleep(500 * time.Millisecond)

	client, err := NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, msgType := range []string{"one", "two"} {
		raw, err := client.Call(context.Background(), q.Keys[0], []byte(`{"type":"`+msgType+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		var out struct{ Echo string }
		if err := rpc.Decode(raw, &out); err != nil || out.Echo != msgType {
			t.Fatalf("call %s = %+v, %v", msgType, out, err)
		}
	}
}
