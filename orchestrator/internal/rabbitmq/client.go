package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log"
	"orchestrator/internal/messaging"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrClosed            = messaging.ErrClosed
	ErrOverloaded        = messaging.ErrOverloaded
	ErrUnroutable        = messaging.ErrUnroutable
	ErrBrokerNack        = messaging.ErrBrokerNack
	ErrConnectionLost    = messaging.ErrConnectionLost
	ErrMalformedResponse = messaging.ErrMalformedResponse
)

type pending struct {
	response chan []byte
	failure  chan error
}

// Client owns distinct channels for topology, confirms, replies, and events.
// Publishing is serialized because AMQP confirms are delivery-tag ordered per channel;
// this keeps confirmation/return correlation bounded and cancellation-safe.
type Client struct {
	conn                                 *amqp.Connection
	topology, publisher, replies, events *amqp.Channel
	replyQueue                           string
	requestTimeout                       time.Duration
	slots                                chan struct{}
	mu                                   sync.Mutex
	pending                              map[string]*pending
	publisherGate                        chan struct{}
	returned                             <-chan amqp.Return
	done                                 chan struct{}
	unavailable                          chan struct{}
	closeOnce                            sync.Once
	unavailableOnce                      sync.Once
	ready                                atomic.Bool
}

func New(url string, maxInFlight int, requestTimeout time.Duration) (*Client, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial RabbitMQ: %w", err)
	}
	c, err := NewWithConnection(conn, maxInFlight, requestTimeout)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func NewWithConnection(conn *amqp.Connection, maxInFlight int, requestTimeout time.Duration) (*Client, error) {
	if maxInFlight <= 0 {
		return nil, fmt.Errorf("max in-flight RPC calls must be positive")
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("RabbitMQ request timeout must be positive")
	}
	c := &Client{
		conn:           conn,
		requestTimeout: requestTimeout,
		slots:          make(chan struct{}, maxInFlight),
		pending:        map[string]*pending{},
		publisherGate:  make(chan struct{}, 1),
		done:           make(chan struct{}),
		unavailable:    make(chan struct{}),
	}
	var err error
	if c.topology, err = conn.Channel(); err != nil {
		return nil, err
	}
	if c.publisher, err = conn.Channel(); err != nil {
		_ = c.topology.Close()
		return nil, err
	}
	if err = c.publisher.Confirm(false); err != nil {
		_ = c.publisher.Close()
		_ = c.topology.Close()
		return nil, err
	}
	c.returned = c.publisher.NotifyReturn(make(chan amqp.Return, 1))
	if c.replies, err = conn.Channel(); err != nil {
		_ = c.publisher.Close()
		_ = c.topology.Close()
		return nil, err
	}
	if c.events, err = conn.Channel(); err != nil {
		_ = c.replies.Close()
		_ = c.publisher.Close()
		_ = c.topology.Close()
		return nil, err
	}
	q, err := c.replies.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	c.replyQueue = q.Name
	deliveries, err := c.replies.Consume(q.Name, "orchestrator-rpc-replies", false, true, false, false, nil)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	c.ready.Store(true)
	go c.dispatchReplies(deliveries)
	go c.watchClose(c.topology.NotifyClose(make(chan *amqp.Error, 1)))
	go c.watchClose(c.publisher.NotifyClose(make(chan *amqp.Error, 1)))
	go c.watchClose(c.replies.NotifyClose(make(chan *amqp.Error, 1)))
	go c.watchClose(c.events.NotifyClose(make(chan *amqp.Error, 1)))
	go c.watchConsumerCancel(c.events.NotifyCancel(make(chan string, 1)))
	go c.watchClose(conn.NotifyClose(make(chan *amqp.Error, 1)))
	return c, nil
}

func (c *Client) Ready() bool                    { return c != nil && c.ready.Load() }
func (c *Client) Unavailable() <-chan struct{}   { return c.unavailable }
func (c *Client) EventChannel() *amqp.Channel    { return c.events }
func (c *Client) TopologyChannel() *amqp.Channel { return c.topology }

func (c *Client) dispatchReplies(deliveries <-chan amqp.Delivery) {
	for d := range deliveries {
		id := d.CorrelationId
		c.mu.Lock()
		p, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if !ok {
			log.Printf("RabbitMQ late/unknown reply correlation_id=%s", id)
			_ = d.Ack(false)
			continue
		}
		select {
		case p.response <- append([]byte(nil), d.Body...):
		default:
		}
		_ = d.Ack(false)
	}
	c.markUnavailable(ErrConnectionLost)
}
func (c *Client) watchClose(ch <-chan *amqp.Error) {
	select {
	case <-c.done:
		return
	case <-ch:
		c.markUnavailable(ErrConnectionLost)
	}
}
func (c *Client) watchConsumerCancel(ch <-chan string) {
	select {
	case <-c.done:
		return
	case <-ch:
		c.markUnavailable(ErrConnectionLost)
	}
}
func (c *Client) markUnavailable(err error) {
	c.unavailableOnce.Do(func() {
		c.ready.Store(false)
		close(c.unavailable)
		c.failAll(err)
	})
}
func (c *Client) failAll(err error) {
	c.mu.Lock()
	all := c.pending
	c.pending = map[string]*pending{}
	c.mu.Unlock()
	for _, p := range all {
		select {
		case p.failure <- err:
		default:
		}
	}
}
func (c *Client) acquire(ctx context.Context) error {
	if !c.Ready() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return contextError(err)
	}
	select {
	case c.slots <- struct{}{}:
		return nil
	default:
		return ErrOverloaded
	}
}
func (c *Client) release() { <-c.slots }
func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return context.Canceled
}

func expiration(ctx context.Context) (string, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", errors.New("RabbitMQ publish deadline is required")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "", context.DeadlineExceeded
	}
	ms := remaining.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return strconv.FormatInt(ms, 10), nil
}

func (c *Client) boundedContext(parent context.Context) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, c.requestTimeout)
}

func (c *Client) Call(ctx context.Context, routingKey string, body []byte) ([]byte, error) {
	ctx, cancel := c.boundedContext(ctx)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	correlationID := uuid.NewString()
	p := &pending{response: make(chan []byte, 1), failure: make(chan error, 1)}
	c.mu.Lock()
	if !c.Ready() {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.pending[correlationID] = p
	c.mu.Unlock()
	cleanup := func() { c.mu.Lock(); delete(c.pending, correlationID); c.mu.Unlock() }
	if err := c.publish(ctx, routingKey, body, correlationID, c.replyQueue); err != nil {
		cleanup()
		return nil, err
	}
	select {
	case reply := <-p.response:
		return reply, nil
	case err := <-p.failure:
		return nil, err
	case <-ctx.Done():
		cleanup()
		return nil, contextError(ctx.Err())
	case <-c.done:
		cleanup()
		return nil, ErrClosed
	case <-c.unavailable:
		cleanup()
		return nil, ErrConnectionLost
	}
}
func (c *Client) Send(ctx context.Context, routingKey string, body []byte) error {
	ctx, cancel := c.boundedContext(ctx)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	return c.publish(ctx, routingKey, body, uuid.NewString(), "")
}

func (c *Client) publish(ctx context.Context, routingKey string, body []byte, correlationID, replyTo string) error {
	if !c.Ready() {
		return ErrClosed
	}
	expires, err := expiration(ctx)
	if err != nil {
		return err
	}
	select {
	case c.publisherGate <- struct{}{}:
		defer func() { <-c.publisherGate }()
	case <-ctx.Done():
		return contextError(ctx.Err())
	case <-c.unavailable:
		return ErrConnectionLost
	case <-c.done:
		return ErrClosed
	}
	if !c.Ready() {
		return ErrClosed
	}
	messageID := uuid.NewString()
	confirmation, err := c.publisher.PublishWithDeferredConfirm(
		"clearsky.commands.v1",
		routingKey,
		true,
		false,
		amqp.Publishing{
			ContentType:   "application/json",
			DeliveryMode:  amqp.Persistent,
			CorrelationId: correlationID,
			MessageId:     messageID,
			ReplyTo:       replyTo,
			Expiration:    expires,
			Body:          body,
		},
	)
	if err != nil {
		c.markUnavailable(ErrConnectionLost)
		return fmt.Errorf("publish: %w", err)
	}
	if confirmation == nil {
		c.markUnavailable(ErrConnectionLost)
		return ErrConnectionLost
	}
	// RabbitMQ sends basic.return before the confirmation for the same mandatory publish.
	// The return listener is synchronously notified before the per-message confirmation is
	// completed. Serializing publishers therefore makes the return stream unambiguous.
	for {
		select {
		case ret, ok := <-c.returned:
			if !ok {
				c.markUnavailable(ErrConnectionLost)
				return ErrConnectionLost
			}
			if ret.MessageId == messageID || ret.CorrelationId == correlationID {
				return ErrUnroutable
			}
			c.markUnavailable(ErrConnectionLost)
			return ErrConnectionLost
		case <-confirmation.Done():
			if !confirmation.Acked() {
				if !c.Ready() {
					return ErrConnectionLost
				}
				return ErrBrokerNack
			}
			select {
			case ret, ok := <-c.returned:
				if !ok {
					c.markUnavailable(ErrConnectionLost)
					return ErrConnectionLost
				}
				if ret.MessageId == messageID || ret.CorrelationId == correlationID {
					return ErrUnroutable
				}
				c.markUnavailable(ErrConnectionLost)
				return ErrConnectionLost
			default:
			}
			return nil
		case <-ctx.Done():
			// PublishWithDeferredConfirm cannot cancel an AMQP write. A late
			// return/confirmation would be ambiguous for a later publish, so this
			// client must not publish again until it is recreated.
			c.markUnavailable(ErrConnectionLost)
			return contextError(ctx.Err())
		case <-c.unavailable:
			return ErrConnectionLost
		case <-c.done:
			return ErrClosed
		}
	}
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.markUnavailable(ErrClosed)
		if c.events != nil {
			_ = c.events.Close()
		}
		if c.replies != nil {
			_ = c.replies.Close()
		}
		if c.publisher != nil {
			_ = c.publisher.Close()
		}
		if c.topology != nil {
			_ = c.topology.Close()
		}
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
	return nil
}
