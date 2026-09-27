// Package amqpx is the RabbitMQ plumbing shared by the services: an RPC
// server for a service's owned queue, and a publisher with confirms.
package amqpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"clearsky/contracts/obs"
	"clearsky/contracts/rpc"
	"clearsky/contracts/topology"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Handler processes one message. msgType is the body's "type" field and body
// the complete JSON. The result becomes the reply's data; an *rpc.Error
// becomes an error reply, any other error an INTERNAL_ERROR reply.
type Handler func(ctx context.Context, msgType string, body json.RawMessage) (any, error)

// Server consumes one owned queue.
type Server struct {
	Queue   topology.Queue
	Handler Handler
	Workers int           // concurrent handlers; also the prefetch count
	Timeout time.Duration // per message; default 30s
	Service string        // for spans and logs
	Declare bool          // declare the queue topology on start (default true via NewServer)
	replyMu sync.Mutex
}

// NewServer returns a server that declares its queue on start.
func NewServer(service string, queue topology.Queue, workers int, handler Handler) *Server {
	if workers < 1 {
		workers = 1
	}
	return &Server{Queue: queue, Handler: handler, Workers: workers, Timeout: 30 * time.Second, Service: service, Declare: true}
}

// Serve consumes until ctx is cancelled (graceful: in-flight messages finish)
// or the channel closes (returns an error; the process should exit and be
// restarted by the orchestrator of containers).
func (s *Server) Serve(ctx context.Context, conn *amqp.Connection) error {
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()
	if s.Declare {
		if err := topology.Declare(ch, s.Queue); err != nil {
			return err
		}
	}
	if err := ch.Qos(s.Workers, 0, false); err != nil {
		return fmt.Errorf("set prefetch: %w", err)
	}
	consumerTag := fmt.Sprintf("%s-%d", s.Service, time.Now().UnixNano())
	deliveries, err := ch.Consume(s.Queue.Name, consumerTag, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume %s: %w", s.Queue.Name, err)
	}
	closed := ch.NotifyClose(make(chan *amqp.Error, 1))

	var wg sync.WaitGroup
	jobs := make(chan amqp.Delivery)
	for i := 0; i < s.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				s.handle(ch, d)
			}
		}()
	}
	defer func() {
		close(jobs)
		wg.Wait()
	}()

	slog.Info("consuming", "queue", s.Queue.Name, "workers", s.Workers)
	for {
		select {
		case <-ctx.Done():
			_ = ch.Cancel(consumerTag, false)
			return nil
		case amqpErr := <-closed:
			if amqpErr == nil {
				return errors.New("channel closed")
			}
			return fmt.Errorf("channel closed: %w", amqpErr)
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			jobs <- d
		}
	}
}

func (s *Server) handle(ch *amqp.Channel, d amqp.Delivery) {
	ctx, cancel := context.WithTimeout(obs.ExtractAMQP(context.Background(), d.Headers), s.Timeout)
	defer cancel()

	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(d.Body, &probe)
	ctx, span := obs.Tracer(s.Service).Start(ctx, "rpc "+s.Queue.Keys[0]+" "+probe.Type,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", s.Queue.Name),
			attribute.String("rpc.type", probe.Type)))
	defer span.End()

	var reply []byte
	result, err := s.safeHandle(ctx, probe.Type, d.Body)
	if err != nil {
		rpcErr, ok := rpc.AsError(err)
		if !ok {
			slog.ErrorContext(ctx, "handler failed", "type", probe.Type, "error", err)
			rpcErr = rpc.Fail(rpc.CodeInternal, "The operation failed")
		}
		span.SetStatus(codes.Error, rpcErr.Code)
		span.SetAttributes(attribute.String("rpc.error_code", rpcErr.Code))
		reply = rpc.Failure(rpcErr)
		// A message nobody waits for cannot report the error: dead-letter it,
		// unless it may succeed later.
		if d.ReplyTo == "" {
			if rpcErr.Retryable {
				time.Sleep(time.Second) // avoid a hot redelivery loop
				_ = d.Nack(false, true)
			} else {
				_ = d.Nack(false, false)
			}
			return
		}
	} else if d.ReplyTo != "" {
		if reply, err = rpc.Success(result); err != nil {
			slog.ErrorContext(ctx, "encode reply", "type", probe.Type, "error", err)
			reply = rpc.Failure(rpc.Fail(rpc.CodeInternal, "The operation failed"))
		}
	}

	if d.ReplyTo != "" {
		s.replyMu.Lock()
		err := ch.PublishWithContext(ctx, "", d.ReplyTo, false, false, amqp.Publishing{
			ContentType:   "application/json",
			CorrelationId: d.CorrelationId,
			Headers:       obs.InjectAMQP(ctx, nil),
			Body:          reply,
		})
		s.replyMu.Unlock()
		if err != nil {
			slog.ErrorContext(ctx, "publish reply", "error", err)
			_ = d.Nack(false, true)
			return
		}
	}
	_ = d.Ack(false)
}

// safeHandle turns a handler panic into an internal error instead of
// killing the worker.
func (s *Server) safeHandle(ctx context.Context, msgType string, body []byte) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return s.Handler(ctx, msgType, body)
}

// Dial connects to RabbitMQ, retrying while the broker starts.
func Dial(ctx context.Context, url string) (*amqp.Connection, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		conn, err := amqp.Dial(url)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("connect to RabbitMQ: %w", lastErr)
}
