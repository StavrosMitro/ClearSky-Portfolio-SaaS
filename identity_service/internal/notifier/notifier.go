// Package notifier hands account emails to the notifications service.
package notifier

import (
	"context"
	"encoding/json"

	"identity_service/internal/accounts"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/messages"
	"clearsky/contracts/topology"

	"github.com/google/uuid"
)

// Publisher implements accounts.Mailer. Publish returns once RabbitMQ has
// stored the request, so a failure rolls back the link being created.
type Publisher struct{ Pub *amqpx.Publisher }

func (p Publisher) Send(ctx context.Context, msg accounts.Message) error {
	body, err := json.Marshal(messages.EmailRequest{
		Type:          messages.TypeEmail,
		DedupeKey:     msg.Template + ":" + uuid.NewString(),
		InstitutionID: msg.InstitutionID,
		Recipient:     msg.To,
		Template:      msg.Template,
		Subject:       msg.Subject,
		Body:          msg.Body,
	})
	if err != nil {
		return err
	}
	return p.Pub.Publish(ctx, topology.KeyNotifications, body)
}
