// Package topology names every exchange, queue and routing key, and declares
// a service's owned queue with its dead-letter queue.
package topology

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	CommandsExchange   = "clearsky.commands.v1" // direct: RPC requests and sync messages
	EventsExchange     = "clearsky.events.v1"   // topic: domain events
	DeadLetterExchange = "clearsky.dlx.v1"      // direct: rejected messages
)

// Queue is one owned command queue and the routing keys bound to it.
type Queue struct {
	Name string
	Keys []string
}

// Owned queues. Each routing key has exactly one owner.
var (
	Identity      = Queue{"clearsky.identity.commands.v1", []string{"auth.request"}}
	Institutions  = Queue{"clearsky.institutions.commands.v1", []string{"institutions.request"}}
	GradesIngest  = Queue{"clearsky.grades-ingest.commands.v1", []string{"grades.ingest.request"}}
	GradesQuery   = Queue{"clearsky.grades-query.commands.v1", []string{"grades.query.request"}}
	GradesSync    = Queue{"clearsky.grades-query.sync.v1", []string{"grades.query.sync"}}
	Reviews       = Queue{"clearsky.reviews.commands.v1", []string{"reviews.request"}}
	ReviewsSync   = Queue{"clearsky.reviews.sync.v1", []string{"reviews.sync"}}
	Notifications = Queue{"clearsky.notifications.email.v1", []string{"notifications.email"}}
)

// Routing keys used by callers.
const (
	KeyIdentity      = "auth.request"
	KeyInstitutions  = "institutions.request"
	KeyGradesIngest  = "grades.ingest.request"
	KeyGradesQuery   = "grades.query.request"
	KeyGradesSync    = "grades.query.sync"
	KeyReviews       = "reviews.request"
	KeyReviewsSync   = "reviews.sync"
	KeyNotifications = "notifications.email"
)

// All lists every owned queue (used by topology tests and documentation).
func All() []Queue {
	return []Queue{Identity, Institutions, GradesIngest, GradesQuery, GradesSync, Reviews, ReviewsSync, Notifications}
}

// DeclareExchanges declares the shared exchanges (idempotent).
func DeclareExchanges(ch *amqp.Channel) error {
	for name, kind := range map[string]string{CommandsExchange: "direct", EventsExchange: "topic", DeadLetterExchange: "direct"} {
		if err := ch.ExchangeDeclare(name, kind, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare exchange %s: %w", name, err)
		}
	}
	return nil
}

// Declare declares q, its dead-letter queue and all bindings (idempotent).
func Declare(ch *amqp.Channel, q Queue) error {
	if err := DeclareExchanges(ch); err != nil {
		return err
	}
	args := amqp.Table{"x-dead-letter-exchange": DeadLetterExchange, "x-dead-letter-routing-key": q.Name + ".dead"}
	if _, err := ch.QueueDeclare(q.Name, true, false, false, false, args); err != nil {
		return fmt.Errorf("declare queue %s: %w", q.Name, err)
	}
	if _, err := ch.QueueDeclare(q.Name+".dlq", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare queue %s.dlq: %w", q.Name, err)
	}
	if err := ch.QueueBind(q.Name+".dlq", q.Name+".dead", DeadLetterExchange, false, nil); err != nil {
		return fmt.Errorf("bind %s.dlq: %w", q.Name, err)
	}
	for _, key := range q.Keys {
		if err := ch.QueueBind(q.Name, key, CommandsExchange, false, nil); err != nil {
			return fmt.Errorf("bind %s to %s: %w", key, q.Name, err)
		}
	}
	return nil
}
