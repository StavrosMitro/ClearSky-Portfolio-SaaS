package mq

import (
	"fmt"
	"os"

	"github.com/streadway/amqp"
)

var Mqconn *amqp.Connection
var Mqch *amqp.Channel

func InitRabbitMQ() error {
	var err error
	url := os.Getenv("AMQP_URL")
	if url == "" {
		// Standalone development only; the root stack always sets AMQP_URL.
		url = "amqp://guest:guest@rabbitmq:5672/"
		fmt.Println("AMQP_URL is not set; using the development default")
	}
	Mqconn, err = amqp.Dial(url)
	if err != nil {
		fmt.Println("Failed to connect to RabbitMQ:", err)
		return err
	}
	fmt.Println("RabbitMQ connection initialized.")

	Mqch, err = Mqconn.Channel()
	if err != nil {
		fmt.Println("Failed to open a channel:", err)
		return err
	}
	fmt.Println("RabbitMQ Channel initialized.")
	return nil
}
