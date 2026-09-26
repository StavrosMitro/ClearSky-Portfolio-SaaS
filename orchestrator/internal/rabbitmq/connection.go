package rabbitmq

import (
	"fmt"
	"orchestrator/internal/config"

	amqp "github.com/rabbitmq/amqp091-go"
)

// SetupMessaging declares the exchange, queue with DLX, and bindings.
func SetupMessaging(ch *amqp.Channel) error {
	// Declare exchange
	if err := ch.ExchangeDeclare(
		config.Cfg.Exchange.Name,
		config.Cfg.Exchange.Type,
		true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("ExchangeDeclare failed: %w", err)
	}
	if err := ch.ExchangeDeclare("clearsky.commands.v1", "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("commands ExchangeDeclare failed: %w", err)
	}
	if err := ch.ExchangeDeclare("clearsky.dlx.v1", "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("DLX ExchangeDeclare failed: %w", err)
	}
	// Declare queue with DLX settings
	qArgs := amqp.Table{
		"x-dead-letter-exchange":    "clearsky.dlx.v1",
		"x-dead-letter-routing-key": config.Cfg.Queue.Name + ".dead",
	}
	if _, err := ch.QueueDeclare(
		config.Cfg.Queue.Name,
		true, false, false, false,
		qArgs,
	); err != nil {
		return fmt.Errorf("QueueDeclare failed: %w", err)
	}
	// Declare DLQ
	if _, err := ch.QueueDeclare(
		config.Cfg.Queue.DLX,
		true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("DLQ Declare failed: %w", err)
	}
	if err := ch.QueueBind(config.Cfg.Queue.DLX, config.Cfg.Queue.Name+".dead", "clearsky.dlx.v1", false, nil); err != nil {
		return fmt.Errorf("DLQ QueueBind failed: %w", err)
	}
	// Bindings
	for _, key := range config.Cfg.Bindings {
		if err := ch.QueueBind(
			config.Cfg.Queue.Name,
			key,
			config.Cfg.Exchange.Name,
			false, nil,
		); err != nil {
			return fmt.Errorf("QueueBind key '%s' failed: %w", key, err)
		}
	}
	return nil
}
