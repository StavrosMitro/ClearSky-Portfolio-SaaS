package amqpx

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"clearsky/contracts/obs"
	"clearsky/contracts/topology"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrUnroutable means no queue is bound to the routing key.
var ErrUnroutable = errors.New("message is unroutable")

// Publisher sends persistent messages to the commands exchange and waits
// for the broker's confirmation. Publishes are serialised so that a
// returned (unroutable) message can be matched to its publish.
type Publisher struct {
	mu       sync.Mutex
	ch       *amqp.Channel
	returned chan amqp.Return
}

func NewPublisher(conn *amqp.Connection) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	if err := topology.DeclareExchanges(ch); err != nil {
		_ = ch.Close()
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, err
	}
	return &Publisher{ch: ch, returned: ch.NotifyReturn(make(chan amqp.Return, 1))}, nil
}

// Publish sends body with routingKey and returns once RabbitMQ has stored it
// (persistent, confirmed) or failed.
func (p *Publisher) Publish(ctx context.Context, routingKey string, body []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	messageID := uuid.NewString()
	confirmation, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, topology.CommandsExchange, routingKey, true, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    messageID,
		Headers:      obs.InjectAMQP(ctx, nil),
		Body:         body,
	})
	if err != nil {
		return fmt.Errorf("publish %s: %w", routingKey, err)
	}
	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("confirm %s: %w", routingKey, err)
	}
	// basic.return arrives before the confirmation of the same publish.
	select {
	case ret := <-p.returned:
		if ret.MessageId == messageID {
			return fmt.Errorf("%w: %s", ErrUnroutable, routingKey)
		}
	default:
	}
	if !acked {
		return fmt.Errorf("broker rejected %s", routingKey)
	}
	return nil
}

func (p *Publisher) Close() error { return p.ch.Close() }
