// handlers/buy_handler.go
package handlers

import (
	"encoding/json"
	"log"

	"credits_service/dbService"

	amqp "github.com/rabbitmq/amqp091-go"
)

// BuyReq is the payload for a purchase request
type BuyReq struct {
	Name   string `json:"name"`
	Amount int    `json:"amount"`
}

func HandleBuy(d amqp.Delivery, ch *amqp.Channel) {
	var req BuyReq

	if err := json.Unmarshal(d.Body, &req); err != nil {
		log.Printf("Invalid JSON received: %v", err)
		sendBuyReplyAndNack(ch, d, rpcFailure("INVALID_REQUEST", "A valid purchase request is required", false), false)
		return
	}
	if req.Name == "" || req.Amount <= 0 {
		sendBuyReplyAndNack(ch, d, rpcFailure("INVALID_REQUEST", "A valid institution and positive amount are required", false), false)
		return
	}

	success, err := dbService.BuyCredits(req.Name, req.Amount)
	if err != nil {
		log.Printf("DB error during BuyCredits: %v", err)
		sendBuyReplyAndNack(ch, d, rpcFailure("DEPENDENCY_UNAVAILABLE", "Credits store is unavailable", true), true)
		return
	}

	var res RPCEnvelope
	if success {
		res = rpcSuccess(map[string]string{"message": "Credits purchased successfully"})
	} else {
		res = rpcFailure("CONFLICT", "Credits could not be purchased", false)
	}

	if err := publishBuyReply(ch, d, res); err != nil {
		log.Printf("Failed to publish reply: %v", err)
		d.Nack(false, true)
		return
	}
	d.Ack(false)
}

func sendBuyReplyAndNack(ch *amqp.Channel, d amqp.Delivery, res RPCEnvelope, requeue bool) {
	if err := publishBuyReply(ch, d, res); err != nil {
		log.Printf("Failed to publish error response: %v", err)
	}
	d.Nack(false, requeue)
}

func publishBuyReply(ch *amqp.Channel, d amqp.Delivery, res RPCEnvelope) error {
	if d.ReplyTo == "" {
		return nil
	}

	body, err := json.Marshal(res)
	if err != nil {
		return err
	}

	return ch.Publish(
		"",        // default exchange
		d.ReplyTo, // routing key (callback queue)
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType:   "application/json",
			CorrelationId: d.CorrelationId,
			Body:          body,
		},
	)
}
