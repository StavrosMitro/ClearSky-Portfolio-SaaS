package query

import (
	"context"
	"encoding/json"

	"clearsky/contracts/messages"
	"clearsky/contracts/rpc"
)

type request struct {
	Viewer
	GradingID string `json:"grading_id"`
}

// Handle serves grades.query.request (reads).
func (s *Service) Handle(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
	var req request
	if err := rpc.Bind(body, &req); err != nil {
		return nil, err
	}
	switch msgType {
	case "visible_gradings":
		return s.Visible(ctx, req.Viewer)
	case "distributions":
		return s.DistributionsFor(ctx, req.Viewer, req.GradingID)
	case "student_grades":
		return s.StudentGrades(ctx, req.Viewer)
	case "has_grade":
		return s.HasGrade(ctx, req.Viewer, req.GradingID)
	default:
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown grades query")
	}
}

// HandleSync applies grades.query.sync messages (from the orchestrator).
func (s *Service) HandleSync(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
	if msgType != messages.TypeGradingSnapshot {
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown synchronisation message")
	}
	var snap messages.GradingSnapshot
	if err := rpc.Bind(body, &snap); err != nil {
		return nil, err
	}
	applied, err := s.Apply(ctx, snap)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"applied": applied}, nil
}
