package institutions

import (
	"context"
	"encoding/json"

	"clearsky/contracts/rpc"
)

// request is the union of all operation fields. The gateway fills
// institution_id and actor_user_id from the caller's verified JWT.
type request struct {
	InstitutionID  string `json:"institution_id"`
	ActorUserID    string `json:"actor_user_id"`
	Name           string `json:"name"`
	ContactEmail   string `json:"contact_email"`
	Director       string `json:"director"`
	Amount         int    `json:"amount"`
	IdempotencyKey string `json:"idempotency_key"`
	GradingID      string `json:"grading_id"`
	Limit          int    `json:"limit"`
}

// Handle dispatches institutions.request messages.
func (s *Service) Handle(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
	var req request
	if err := rpc.Bind(body, &req); err != nil {
		return nil, err
	}
	switch msgType {
	case "register":
		return s.Register(ctx, req.InstitutionID, req.Name, req.ContactEmail, req.Director)
	case "get":
		return s.Get(ctx, req.InstitutionID)
	case "list":
		return s.List(ctx)
	case "purchase":
		return s.Purchase(ctx, req.InstitutionID, req.Amount, req.IdempotencyKey, req.ActorUserID)
	case "charge":
		return s.Charge(ctx, req.InstitutionID, req.GradingID, req.ActorUserID)
	case "history":
		return s.History(ctx, req.InstitutionID, req.Limit)
	default:
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown institutions operation")
	}
}
