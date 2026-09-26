// handler.go
package handlers

import (
	"encoding/json"
	"log"
	"registration_service/dbService"

	amqp "github.com/rabbitmq/amqp091-go"
)

// UserRequest mirrors the JSON that comes over the wire
type UserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Director string `json:"director"`
}

// Response is sent back to the orchestrator
type RPCError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Response struct {
	Version int       `json:"version"`
	Data    any       `json:"data,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

func HandleRegister(d amqp.Delivery, ch *amqp.Channel) {
	log.Println("→ HandleRegister called")
	// Acknowledge the message no matter what (multiple = false)
	defer func() {
		d.Ack(false)
		log.Println("… Message acknowledged")
	}()

	var req UserRequest
	var res Response

	// 1. Parse JSON ---------------------------------------------------------
	log.Println("… Parsing JSON payload")
	if err := json.Unmarshal(d.Body, &req); err != nil {
		log.Printf("❌ JSON unmarshal error: %v", err)
		res = Response{Version: 1, Error: &RPCError{Code: "INVALID_REQUEST", Message: "A valid institution request is required"}}
		publishReply(ch, d, res)
		return
	}
	if req.Name == "" || req.Email == "" || req.Director == "" {
		res = Response{Version: 1, Error: &RPCError{Code: "INVALID_REQUEST", Message: "Name, email and director are required"}}
		publishReply(ch, d, res)
		return
	}
	log.Printf("✅ Parsed UserRequest: %+v", req)

	// 2. Business logic -----------------------------------------------------
	log.Println("… Calling dbService.AddInstitution")
	code, err := dbService.AddInstitution(req.Name, req.Email, req.Director)
	if err != nil {
		if code == 2 {
			log.Printf("⚠ Conflict: institution %q already registered", req.Name)
			res = Response{Version: 1, Error: &RPCError{Code: "CONFLICT", Message: "Institution already registered"}}
		} else {
			log.Printf("❌ Database error for %q: %v", req.Name, err)
			res = Response{Version: 1, Error: &RPCError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Institution store is unavailable", Retryable: true}}
		}
		publishReply(ch, d, res)
		return
	}

	// 3. Success ------------------------------------------------------------
	log.Printf("✅ Institution %q registered (code %d)", req.Name, code)
	res = Response{Version: 1, Data: map[string]string{"message": "Institution registered successfully"}}
	publishReply(ch, d, res)
}

func publishReply(ch *amqp.Channel, d amqp.Delivery, res Response) {
	if d.ReplyTo == "" {
		log.Println("… No ReplyTo set; skipping reply publish")
		return
	}

	body, _ := json.Marshal(res)
	log.Printf("… Publishing reply to %q (CorrelationId=%s): %+v", d.ReplyTo, d.CorrelationId, res)

	err := ch.Publish(
		"",        // default exchange
		d.ReplyTo, // queue the caller named
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType:   "application/json",
			CorrelationId: d.CorrelationId,
			Body:          body,
		},
	)
	if err != nil {
		log.Printf("❌ Failed to publish reply: %v", err)
	} else {
		log.Println("✅ Reply published")
	}
}
