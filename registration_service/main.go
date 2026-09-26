package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"registration_service/dbService"
	"registration_service/handlers"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
)

func failOnErr(err error, msg string) {
	if err != nil {
		log.Fatalf("❌ %s: %v", msg, err)
	}
}

func main() {
	log.Println("→ Starting registration service")

	// ----------------------------------------------------------------------
	// 1. Environment & DB init
	// ----------------------------------------------------------------------
	if err := godotenv.Load(); err != nil {
		log.Println("⚠ No .env file found; relying on system environment variables.")
	} else {
		log.Println("✅ .env file loaded")
	}

	log.Println("… Initializing database connection")
	dbService.InitDB()
	log.Println("✅ Database initialized")

	// ----------------------------------------------------------------------
	// 2. Rabbit MQ connection / channel
	// ----------------------------------------------------------------------
	rmqURL := os.Getenv("RABBITMQ_URL")
	if rmqURL == "" {
		rmqURL = "amqp://guest:guest@localhost:5672/"
		log.Printf("⚠ RABBITMQ_URL not set, using default %s", rmqURL)
	} else {
		log.Printf("✅ Using RabbitMQ URL: %s", rmqURL)
	}

	conn, err := amqp.Dial(rmqURL)
	failOnErr(err, "Failed to connect to RabbitMQ")
	log.Println("✅ Connected to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	failOnErr(err, "Failed to open channel")
	log.Println("✅ Channel opened")
	defer ch.Close()

	// ----------------------------------------------------------------------
	// 3. Declare exchange & queue (idempotent; safe if already exist)
	// ----------------------------------------------------------------------
	exchange := "clearsky.commands.v1"
	routingKey := "institution.registered"

	log.Printf("… Declaring exchange %q", exchange)
	err = ch.ExchangeDeclare(
		exchange, // name
		"direct", // type
		true,     // durable
		false,    // auto-delete
		false,    // internal
		false,    // no-wait
		nil,      // args
	)
	failOnErr(err, "Failed to declare exchange")
	log.Printf("✅ Exchange %q declared", exchange)

	queueName := "clearsky.registration.commands.v1"
	if err := ch.ExchangeDeclare("clearsky.dlx.v1", "direct", true, false, false, false, nil); err != nil {
		failOnErr(err, "Failed to declare dead-letter exchange")
	}
	args := amqp.Table{"x-dead-letter-exchange": "clearsky.dlx.v1", "x-dead-letter-routing-key": queueName + ".dead"}
	log.Printf("… Declaring queue %q", queueName)
	q, err := ch.QueueDeclare(queueName, true, false, false, false, args)
	failOnErr(err, "Failed to declare queue")
	_, err = ch.QueueDeclare(queueName+".dlq", true, false, false, false, nil)
	failOnErr(err, "Failed to declare DLQ")
	err = ch.QueueBind(queueName+".dlq", queueName+".dead", "clearsky.dlx.v1", false, nil)
	failOnErr(err, "Failed to bind DLQ")
	log.Printf("✅ Queue %q declared", q.Name)

	log.Printf("… Binding queue %q to exchange %q with routing key %q", q.Name, exchange, routingKey)
	err = ch.QueueBind(
		q.Name,     // queue
		routingKey, // routing key
		exchange,   // exchange
		false,
		nil,
	)
	failOnErr(err, "Failed to bind queue")
	log.Printf("✅ Queue %q bound to exchange %q with routing key %q", q.Name, exchange, routingKey)

	// ----------------------------------------------------------------------
	// 4. QoS & consumer
	// ----------------------------------------------------------------------
	log.Println("… Setting QoS (prefetch count = 1)")
	err = ch.Qos(1, 0, false)
	failOnErr(err, "Failed to set QoS")
	log.Println("✅ QoS set")

	log.Printf("… Registering consumer on queue %q", q.Name)
	msgs, err := ch.Consume(
		q.Name,
		"registration_service", // consumer tag
		false,                  // auto-ack -> false (we ack manually)
		false,                  // exclusive
		false,                  // no-local
		false,                  // no-wait
		nil,                    // args
	)
	failOnErr(err, "Failed to register consumer")
	log.Printf("✅ Consumer registered on queue %q", q.Name)

	// ----------------------------------------------------------------------
	// 5. Graceful shutdown handling
	// ----------------------------------------------------------------------
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// ----------------------------------------------------------------------
	// 6. Message loop
	// ----------------------------------------------------------------------
	const workers = 2

	log.Println("⭐ Waiting for registration messages …")
	for i := 0; i < workers; i++ {
		go func(id int) {
			log.Printf("👷 Worker %d is ready", id)
			for d := range msgs {
				log.Printf("👷 Worker %d received a message", id)
				handlers.HandleRegister(d, ch)
			}
			log.Printf("👷 Worker %d exiting", id)
		}(i)
	}

	<-sigs
	log.Println("🚦 Shutdown requested, exiting …")
}
