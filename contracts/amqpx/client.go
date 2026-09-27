package amqpx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"clearsky/contracts/obs"
	"clearsky/contracts/topology"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// directReplyTo is RabbitMQ's pseudo-queue for RPC replies without a queue.
const directReplyTo = "amq.rabbitmq.reply-to"

// Client makes RPC calls between services (e.g. grades-query asking
// grades-ingest during reconciliation). Calls are serialised: it is meant
// for occasional service-to-service requests, not for the public hot path,
// which goes through the orchestrator's client.
type Client struct {
	mu      sync.Mutex
	ch      *amqp.Channel
	replies <-chan amqp.Delivery
}

func NewClient(conn *amqp.Connection) (*Client, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	replies, err := ch.Consume(directReplyTo, "", true, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return nil, err
	}
	return &Client{ch: ch, replies: replies}, nil
}

// Call publishes body to routingKey and waits for the reply envelope.
func (c *Client) Call(ctx context.Context, routingKey string, body []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	correlationID := uuid.NewString()
	if err := c.ch.PublishWithContext(ctx, topology.CommandsExchange, routingKey, false, false, amqp.Publishing{
		ContentType:   "application/json",
		CorrelationId: correlationID,
		ReplyTo:       directReplyTo,
		Headers:       obs.InjectAMQP(ctx, nil),
		Body:          body,
	}); err != nil {
		return nil, fmt.Errorf("publish %s: %w", routingKey, err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case d, ok := <-c.replies:
			if !ok {
				return nil, errors.New("reply channel closed")
			}
			if d.CorrelationId == correlationID {
				return d.Body, nil
			}
			// A late reply to an earlier, timed-out call: ignore it.
		}
	}
}

func (c *Client) Close() error { return c.ch.Close() }
