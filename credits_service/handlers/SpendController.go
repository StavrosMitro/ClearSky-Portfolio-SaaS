package handlers

import (
	"encoding/json"
	"log"

	"credits_service/dbService"

	amqp "github.com/rabbitmq/amqp091-go"
)

type SpendReq struct {
	Name   string `json:"name"`
	Amount int    `json:"amount"` // Capitalized & correct type
	// code int `json:"code"`
}

func Spending(d amqp.Delivery, ch *amqp.Channel) {
	log.Printf("[Spending] Received message. CorrelationID=%s, ReplyTo=%s", d.CorrelationId, d.ReplyTo)

	var req SpendReq
	var res RPCEnvelope

	// Ensure the message is acknowledged at the end, no matter what.
	defer func() {
		if err := d.Ack(false); err != nil {
			log.Printf("[Spending] Failed to ack message: %v", err)
		}
	}()

	// 1. Parse JSON ---------------------------------------------------------
	if err := json.Unmarshal(d.Body, &req); err != nil {
		log.Printf("[Spending] JSON unmarshal error: %v | Body=%s", err, string(d.Body))
		res = rpcFailure("INVALID_REQUEST", "A valid spending request is required", false)
		publishReply(ch, d, res)
		return
	}
	log.Printf("[Spending] Parsed request: %+v", req)

	// 2. Attempt to diminish credits ---------------------------------------
	isComplete, err := dbService.Diminish(req.Name, req.Amount)
	log.Printf("[Spending] dbService.Diminish(Name=%s, Amount=%d) => isComplete=%t, err=%v", req.Name, req.Amount, isComplete, err)

	if err != nil {
		res = rpcFailure("INSUFFICIENT_CREDITS", "Not enough credits", false)
		publishReply(ch, d, res)
		return
	}

	if isComplete {
		res = rpcSuccess(map[string]string{"message": "Credits spent"})
		publishReply(ch, d, res)
		return
	}

	// If we reach here, it means credits were diminished but not fully consumed (business rule dependent)
	res = rpcFailure("CONFLICT", "Credits could not be fully spent", false)
	publishReply(ch, d, res)
}

func publishReply(ch *amqp.Channel, d amqp.Delivery, res RPCEnvelope) {
	// fire-and-forget call; nothing to send back
	if d.ReplyTo == "" {
		log.Printf("[publishReply] ReplyTo empty; not sending any response. CorrelationID=%s", d.CorrelationId)
		return
	}

	body, errMarshal := json.Marshal(res)
	if errMarshal != nil {
		log.Printf("[publishReply] Failed to marshal response: %v | Response=%+v", errMarshal, res)
		return
	}

	log.Printf("[publishReply] Publishing reply. CorrelationID=%s, Body=%s", d.CorrelationId, string(body))

	if err := ch.Publish(
		"",        // default exchange because we address the queue directly
		d.ReplyTo, // queue the caller named
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType:   "application/json",
			CorrelationId: d.CorrelationId,
			Body:          body,
		},
	); err != nil {
		log.Printf("[publishReply] Failed to publish reply: %v", err)
	}
}
