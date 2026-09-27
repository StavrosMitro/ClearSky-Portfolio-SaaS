package reviews

import (
	"context"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/messages"
	"clearsky/contracts/rpc"
	"clearsky/contracts/topology"
)

// IngestSource reads grading headers from grades-ingest.
type IngestSource struct{ Client *amqpx.Client }

func (s IngestSource) Headers(ctx context.Context) ([]messages.GradingHeader, error) {
	reply, err := s.Client.Call(ctx, topology.KeyGradesIngest, []byte(`{"type":"headers"}`))
	if err != nil {
		return nil, err
	}
	var headers []messages.GradingHeader
	return headers, rpc.Decode(reply, &headers)
}
