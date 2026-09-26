// main.go
package main

import (
	"credits_service/dbService"
	"credits_service/handlers"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
)

func failOnErr(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %v", msg, err)
	}
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found; relying on system enviroment variables")
	}

	dbService.InitDB()

	rmqURL := os.Getenv("RABBITMQ_URL")

	if rmqURL == "" {
		rmqURL = "amqp://guest:guest@localhost:5672/"
	}

	conn, err := amqp.Dial(rmqURL)
	failOnErr(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	failOnErr(err, "Failed to open channel")
	defer ch.Close()

	exchange := "clearsky.commands.v1"
	keys := []string{"credits.avail", "credits.spent", "credits.purchased", "add.new"}

	err = ch.ExchangeDeclare(
		exchange,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	)
	failOnErr(err, "Failed to declare exchange")

	queueName := "clearsky.credits.commands.v1"
	if err := ch.ExchangeDeclare("clearsky.dlx.v1", "direct", true, false, false, false, nil); err != nil {
		failOnErr(err, "Failed to declare dead-letter exchange")
	}
	args := amqp.Table{"x-dead-letter-exchange": "clearsky.dlx.v1", "x-dead-letter-routing-key": queueName + ".dead"}
	q, err := ch.QueueDeclare(queueName, true, false, false, false, args)
	failOnErr(err, "Failed to declare queue")
	_, err = ch.QueueDeclare(queueName+".dlq", true, false, false, false, nil)
	failOnErr(err, "Failed to declare DLQ")
	err = ch.QueueBind(queueName+".dlq", queueName+".dead", "clearsky.dlx.v1", false, nil)
	failOnErr(err, "Failed to bind DLQ")

	for _, key := range keys { //creating 3 bind, 1 per routing key
		if err := ch.QueueBind(
			q.Name,
			key,
			exchange,
			false,
			nil,
		); err != nil {
			failOnErr(err, "Failed to bind queue to key "+key)
		}
	}

	err = ch.Qos(3, 0, false) // three message at a time
	failOnErr(err, "Failed to set QoS")

	msgs, err := ch.Consume(
		q.Name,
		"credits_consumer", // consumer tag
		false,              // auto-ack -> false (we ack manually)
		false,              // exclusive
		false,              // no-local
		false,              // no-wait
		nil,                // args
	)
	failOnErr(err, "Failed to register consumer")

	// ----------------------------------------------------------------------
	// 5. Graceful shutdown handling
	// ----------------------------------------------------------------------
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	const workers = 5

	log.Println("Waiting for credits messages")

	for i := 0; i < workers; i++ {
		go worker(i, msgs, ch)
	}
	log.Printf("%d workers are listening for messages…\n", workers)

	<-sigs
	log.Println("Shutting down")
}

func worker(id int, msgs <-chan amqp.Delivery, ch *amqp.Channel) {
	log.Printf("Worker %d ready", id)
	for d := range msgs {
		switch d.RoutingKey {
		case "credits.spent":
			handlers.Spending(d, ch)
		case "credits.purchased":
			handlers.HandleBuy(d, ch)
		case "credits.avail":
			handlers.AvailableHandler(d, ch)
		case "add.new":
			handlers.AddInstitutionHandler(d, ch)
		default:
			log.Printf("Worker %d: unknown key %q", id, d.RoutingKey)
			d.Nack(false, false)
		}
	}
}
