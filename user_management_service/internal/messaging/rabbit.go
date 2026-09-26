// file: user_management_service/messaging/rabbit.go
package messaging

import (
	"encoding/json"
	"log"
	"os"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	Conn    *amqp.Connection
	Channel *amqp.Channel
)

// Init connects to RabbitMQ, declares exchanges & auth queue+bindings.
func Init() {
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		url = "amqp://guest:guest@rabbitmq:5672/"
	}
	var err error
	for i := 0; i < 10; i++ { // Try 10 times
		Conn, err = amqp.Dial(url)
		if err == nil {
			break
		}
		log.Printf("RabbitMQ dial failed: %v (retrying in 3s)", err)
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		log.Fatalf("RabbitMQ dial: %v", err)
	}

	// Initialize Channel after successful connection
	Channel, err = Conn.Channel()
	if err != nil {
		log.Fatalf("RabbitMQ channel: %v", err)
	}

	// Versioned topology. Old broker resources are intentionally not touched.
	if err := Channel.ExchangeDeclare("clearsky.commands.v1", "direct", true, false, false, false, nil); err != nil {
		log.Fatalf("Declare command exchange: %v", err)
	}
	if err := Channel.ExchangeDeclare("clearsky.events.v1", "topic", true, false, false, false, nil); err != nil {
		log.Fatalf("Declare event exchange: %v", err)
	}
	if err := Channel.ExchangeDeclare("clearsky.dlx.v1", "direct", true, false, false, false, nil); err != nil {
		log.Fatalf("Declare dead-letter exchange: %v", err)
	}
	queue := "clearsky.auth.commands.v1"
	args := amqp.Table{"x-dead-letter-exchange": "clearsky.dlx.v1", "x-dead-letter-routing-key": queue + ".dead"}
	if _, err := Channel.QueueDeclare(queue, true, false, false, false, args); err != nil {
		log.Fatalf("QueueDeclare %s: %v", queue, err)
	}
	if _, err := Channel.QueueDeclare(queue+".dlq", true, false, false, false, nil); err != nil {
		log.Fatalf("QueueDeclare DLQ %s: %v", queue, err)
	}
	if err := Channel.QueueBind(queue+".dlq", queue+".dead", "clearsky.dlx.v1", false, nil); err != nil {
		log.Fatalf("QueueBind DLQ %s: %v", queue, err)
	}
	if err := Channel.QueueBind(queue, "auth.request", "clearsky.commands.v1", false, nil); err != nil {
		log.Fatalf("QueueBind %s: %v", queue, err)
	}
}

// PublishEvent στέλνει ένα event στο clearsky.events.v1 με το δοσμένο routingKey
func PublishEvent(routingKey string, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("PublishEvent marshal: %v", err)
		return
	}
	err = Channel.Publish(
		"clearsky.events.v1",
		routingKey, // routing key
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
	if err != nil {
		log.Printf("PublishEvent publish: %v", err)
	}
}
