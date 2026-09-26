package mq

import (
	"encoding/json"
	"fmt"
	"strings"

	"student_request_review_service/routes"

	"github.com/streadway/amqp"
)

type rpcError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type rpcEnvelope struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func encodeRPCResponse(response string, routeErr error) []byte {
	envelope := rpcEnvelope{Version: 1}
	if routeErr != nil {
		message := strings.ToLower(routeErr.Error())
		switch {
		case strings.Contains(message, "not found"):
			envelope.Error = &rpcError{Code: "NOT_FOUND", Message: "Review was not found"}
		case strings.Contains(message, "missing"), strings.Contains(message, "invalid"), strings.Contains(message, "parse"), strings.Contains(message, "unknown routing"):
			envelope.Error = &rpcError{Code: "INVALID_REQUEST", Message: "Invalid review request"}
		default:
			envelope.Error = &rpcError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Review store is unavailable", Retryable: true}
		}
	} else if json.Valid([]byte(response)) {
		envelope.Data = json.RawMessage(response)
	} else {
		envelope.Error = &rpcError{Code: "INTERNAL_ERROR", Message: "Review service returned invalid data"}
	}
	body, _ := json.Marshal(envelope)
	return body
}

// function to handle errors
func errorHandling(err error, msg string) {
	if err != nil {
		fmt.Printf("%s: %s\n", msg, err)
	}
}

func StartConsumer() {

	// keys for student events
	exchangeKey := "clearsky.commands.v1"
	routingKeysStudent := []string{
		"student.postNewRequest",
		"student.getRequestStatus",
		"student.updateInstructorResponse",
	}

	// declare direct exchange for event routing
	err := Mqch.ExchangeDeclare(
		exchangeKey, // name
		"direct",    // type
		true,        // durable
		false,       // auto-deleted
		false,       // internal
		false,       // no-wait
		nil,         // arguments
	)
	errorHandling(err, "Failed to declare exchange")

	err = Mqch.ExchangeDeclare("clearsky.dlx.v1", "direct", true, false, false, false, nil)
	errorHandling(err, "Failed to declare dead-letter exchange")

	// declare a durable queue
	queue, err := Mqch.QueueDeclare(
		"clearsky.student-review.commands.v1", // queue name
		true,                                  // durable
		false,                                 // delete when unused
		false,                                 // not exclusive
		false,                                 // no-wait
		amqp.Table{"x-dead-letter-exchange": "clearsky.dlx.v1", "x-dead-letter-routing-key": "clearsky.student-review.commands.v1.dead"},
	)
	errorHandling(err, "Failed to declare queue")

	_, err = Mqch.QueueDeclare("clearsky.student-review.commands.v1.dlq", true, false, false, false, nil)
	errorHandling(err, "Failed to declare DLQ")
	err = Mqch.QueueBind("clearsky.student-review.commands.v1.dlq", "clearsky.student-review.commands.v1.dead", "clearsky.dlx.v1", false, nil)
	errorHandling(err, "Failed to bind DLQ")

	// bind the queue to each routing key
	for _, key := range routingKeysStudent {
		err := Mqch.QueueBind(
			queue.Name,
			key,
			exchangeKey,
			false,
			nil,
		)
		errorHandling(err, "Failed to bind queue with key "+key)
	}

	// start consuming messages
	msgs, err := Mqch.Consume(
		queue.Name,
		"student_consumer", // consumer tag
		false,              // manual acks!
		false,              // not exclusive
		false,              // no-local (not supported)
		false,              // no-wait
		nil,
	)
	errorHandling(err, "Failed to register consumer")

	fmt.Println("Consumer Declared.")
	fmt.Printf(" [*] Waiting for messages on: %s\n", queue.Name)

	go func() {
		for d := range msgs {
			response, err := routes.Routing(d.RoutingKey, d.Body)
			if err != nil {
				fmt.Printf("Error processing message for routing key %s: %v", d.RoutingKey, err)
			}
			body := encodeRPCResponse(response, err)
			if d.ReplyTo == "" {
				_ = d.Nack(false, false)
				continue
			}

			err = Mqch.Publish(
				"",        // default exchange for reply
				d.ReplyTo, // reply queue
				false,
				false,
				amqp.Publishing{
					ContentType:   "application/json",
					CorrelationId: d.CorrelationId,
					Body:          body,
				},
			)

			if err != nil {
				fmt.Println("Reply failed.")
				fmt.Println(err)
				d.Nack(false, true) // requeue on publish failure
			} else {
				fmt.Printf("Sent reply to %s\n", d.ReplyTo)
				d.Ack(false)
			}
		}
	}()
}

// FIRST IMPLEMENTATION

/*
	q := "grades.review.requested"

	// Declare queue if not exists.
	_, err := Mqch.QueueDeclare(
		q,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		fmt.Println("Queue declaration failed.")
		fmt.Println(err)

	}
	fmt.Println("Queue declared.")
	fmt.Println(q)

	// Consumer
	msgs, err := Mqch.Consume(
		q,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		fmt.Println("Consumer failed")
		fmt.Println(err)
	}
	fmt.Println("Consumer Declared.")
	fmt.Printf(" [*] Waiting for messages on: %s\n", q)



	go func() {
		for d := range msgs {
			fmt.Printf("Received message: %s", d.Body)

			response, err := routes.Routing(d.Body)
			if err != nil {
				fmt.Println("Error processing message:", err)
				response = fmt.Sprintf(`{"error": "%s"}`, err.Error())
			}
			fmt.Printf("Reply: %s\n", response)

			// Send reply
			err = Mqch.Publish(
				"",
				d.ReplyTo,
				false,
				false,
				amqp.Publishing{
					ContentType:   "application/json",
					CorrelationId: d.CorrelationId,
					Body:          []byte(response),
				},
			)
			if err != nil {
				fmt.Println("Reply failed.")
				fmt.Println(err)
			} else {
				fmt.Printf("Sent reply to %s", d.ReplyTo)
			}
		}
	}()*/
