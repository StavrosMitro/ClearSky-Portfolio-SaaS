// Command reviews runs the grade review service.
package main

import (
	"context"
	"os"
	"time"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/app"
	"clearsky/contracts/topology"

	"reviews_service/internal/reviews"
)

func main() {
	app.Main(app.Options{
		Service:    "reviews",
		Migrations: reviews.Migrations(),
		Setup: func(_ context.Context, env *app.Env) error {
			svc := &reviews.Service{DB: env.DB}
			env.Consume(amqpx.NewServer("reviews", topology.Reviews, 8, svc.Handle))
			env.Consume(amqpx.NewServer("reviews-updater", topology.ReviewsSync, 1, svc.HandleSync))
			client, err := amqpx.NewClient(env.AMQP)
			if err != nil {
				return err
			}
			interval := 5 * time.Minute
			if d, err := time.ParseDuration(os.Getenv("RECONCILE_INTERVAL")); err == nil && d > 0 {
				interval = d
			}
			env.Go(func(ctx context.Context) error {
				return svc.ReconcileLoop(ctx, reviews.IngestSource{Client: client}, interval)
			})
			return nil
		},
	})
}
