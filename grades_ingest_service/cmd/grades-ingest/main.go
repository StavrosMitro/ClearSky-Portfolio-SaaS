// Command grades-ingest runs the service that owns the grades.
package main

import (
	"context"
	"time"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/app"
	"clearsky/contracts/topology"

	"grades_ingest_service/internal/ingest"
)

func main() {
	app.Main(app.Options{
		Service:    "grades-ingest",
		Migrations: ingest.Migrations(),
		Setup: func(_ context.Context, env *app.Env) error {
			svc := &ingest.Service{DB: env.DB}
			// Workers parse workbooks: the queue absorbs bursts in exam periods.
			env.Consume(amqpx.NewServer("grades-ingest", topology.GradesIngest, 4, svc.Handle))
			env.Go(func(ctx context.Context) error { return svc.PurgeLoop(ctx, 10*time.Minute) })
			return nil
		},
	})
}
