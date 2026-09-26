package handlers

import (
	"encoding/json"
	"log"

	"credits_service/dbService"

	amqp "github.com/rabbitmq/amqp091-go"
)

type AvailableReq struct {
	Name string `json:"name"`
}

func AvailableHandler(d amqp.Delivery, ch *amqp.Channel) {
	var req AvailableReq
	log.Printf("We are inside the microservices for return available credits")
	if err := json.Unmarshal(d.Body, &req); err != nil {
		log.Printf("Invalid JSON in AvailableHandler: %v", err)
		sendAvailableReplyAndNack(ch, d, rpcFailure("INVALID_REQUEST", "A valid credits request is required", false), false)
		return
	}
	if req.Name == "" {
		sendAvailableReplyAndNack(ch, d, rpcFailure("INVALID_REQUEST", "Institution name is required", false), false)
		return
	}

	credits, err := dbService.AvailableCredits(req.Name)
	if err != nil {
		log.Printf("DB error in AvailableHandler: %v", err)
		sendAvailableReplyAndNack(ch, d, rpcFailure("DEPENDENCY_UNAVAILABLE", "Credits store is unavailable", true), true)
		return
	}

	res := rpcSuccess(map[string]int{"credits": credits})

	if err := publishAvailableReply(ch, d, res); err != nil {
		log.Printf("Publish reply failed in AvailableHandler: %v", err)
		d.Nack(false, true)
		return
	}
	log.Printf("Available credits %d", credits)
	d.Ack(false)
}

// sendAvailableReplyAndNack publishes the response and nacks the message
func sendAvailableReplyAndNack(ch *amqp.Channel, d amqp.Delivery, res RPCEnvelope, requeue bool) {
	if err := publishAvailableReply(ch, d, res); err != nil {
		log.Printf("Failed to publish AvailableResp: %v", err)
	}
	d.Nack(false, requeue)
}

// publishAvailableReply serializes a response and publishes it to d.ReplyTo.
func publishAvailableReply(ch *amqp.Channel, d amqp.Delivery, res RPCEnvelope) error {
	if d.ReplyTo == "" {
		return nil
	}
	body, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return ch.Publish(
		"",        // default exchange
		d.ReplyTo, // callback queue
		false, false,
		amqp.Publishing{
			ContentType:   "application/json",
			CorrelationId: d.CorrelationId,
			Body:          body,
		},
	)
}
