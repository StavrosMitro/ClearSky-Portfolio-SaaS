package main

import (
	"encoding/json"
	"log"

	"github.com/google/uuid"
	"github.com/streadway/amqp"
)

func main() {
	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatal(err)
	}
	defer ch.Close()

	loginReq := map[string]string{
		"type":       "register",
		"username":   "student2@example.com",
		"password":   "mypassword123",
		"role":       "student",
		"student_id": "STU2024001", // Add student_id for testing
	}
	body, _ := json.Marshal(loginReq)

	corrID := uuid.New().String()
	replyQueue, _ := ch.QueueDeclare("", false, true, true, false, nil)

	err = ch.Publish(
		"clearsky.commands.v1", // exchange
		"auth.request",         // <-- change from "auth.login" to "auth.request"
		false,
		false,
		amqp.Publishing{
			ContentType:   "application/json",
			DeliveryMode:  amqp.Persistent,
			CorrelationId: corrID,
			ReplyTo:       replyQueue.Name,
			Body:          body,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	msgs, _ := ch.Consume(replyQueue.Name, "", true, true, false, false, nil)
	log.Println("Waiting for response...")
	for d := range msgs {
		log.Printf("Received response: %s", d.Body)
		break
	}
}
