package handlers

import (
	"encoding/json"

	"credits_service/dbService"

	amqp "github.com/rabbitmq/amqp091-go"
)

type AddInstitutionReq struct {
	Name string `json:"name"`
	// Credits int    `json:"credits"`
}

func AddInstitutionHandler(d amqp.Delivery, ch *amqp.Channel) {
	var req AddInstitutionReq
	var res RPCEnvelope

	defer d.Ack(false)
	Credits := 10
	// Parse request JSON
	if err := json.Unmarshal(d.Body, &req); err != nil {
		res = rpcFailure("INVALID_REQUEST", "A valid institution request is required", false)
		publishReply(ch, d, res)
		return
	}
	if req.Name == "" {
		res = rpcFailure("INVALID_REQUEST", "Institution name is required", false)
		publishReply(ch, d, res)
		return
	}

	success, err := dbService.NewInstitution(req.Name, Credits)
	if err != nil {
		res = rpcFailure("DEPENDENCY_UNAVAILABLE", "Credits store is unavailable", true)
		publishReply(ch, d, res)
		return
	}

	if success {
		res = rpcSuccess(map[string]string{"message": "Institution added successfully"})
		publishReply(ch, d, res)
		return
	}
	res = rpcFailure("CONFLICT", "Institution already exists", false)
	publishReply(ch, d, res)
}
