package query

import (
	"context"
	"encoding/json"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/messages"
	"clearsky/contracts/rpc"
	"clearsky/contracts/topology"
)

// IngestSource reads from grades-ingest over RabbitMQ.
type IngestSource struct{ Client *amqpx.Client }

func (s IngestSource) call(ctx context.Context, body map[string]string, out any) error {
	raw, _ := json.Marshal(body)
	reply, err := s.Client.Call(ctx, topology.KeyGradesIngest, raw)
	if err != nil {
		return err
	}
	return rpc.Decode(reply, out)
}

func (s IngestSource) Headers(ctx context.Context) ([]messages.GradingHeader, error) {
	var headers []messages.GradingHeader
	err := s.call(ctx, map[string]string{"type": "headers"}, &headers)
	return headers, err
}

func (s IngestSource) Snapshot(ctx context.Context, gradingID string) (messages.GradingSnapshot, error) {
	var snap messages.GradingSnapshot
	err := s.call(ctx, map[string]string{"type": "snapshot", "grading_id": gradingID}, &snap)
	return snap, err
}
