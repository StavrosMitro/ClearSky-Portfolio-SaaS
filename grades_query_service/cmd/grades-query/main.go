// Command grades-query serves grades and statistics and keeps its read
// models in sync with grades-ingest.
package main

import (
	"context"
	"os"
	"time"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/app"
	"clearsky/contracts/topology"

	"grades_query_service/internal/query"
)

func main() {
	app.Main(app.Options{
		Service:    "grades-query",
		Migrations: query.Migrations(),
		Setup: func(_ context.Context, env *app.Env) error {
			svc := &query.Service{DB: env.DB}
			// Reads scale with workers (and replicas); one updater keeps
			// the order of snapshots per grading.
			env.Consume(amqpx.NewServer("grades-query", topology.GradesQuery, 16, svc.Handle))
			env.Consume(amqpx.NewServer("grades-query-updater", topology.GradesSync, 1, svc.HandleSync))
			client, err := amqpx.NewClient(env.AMQP)
			if err != nil {
				return err
			}
			interval := 5 * time.Minute
			if d, err := time.ParseDuration(os.Getenv("RECONCILE_INTERVAL")); err == nil && d > 0 {
				interval = d
			}
			env.Go(func(ctx context.Context) error {
				return svc.ReconcileLoop(ctx, query.IngestSource{Client: client}, interval)
			})
			return nil
		},
	})
}
