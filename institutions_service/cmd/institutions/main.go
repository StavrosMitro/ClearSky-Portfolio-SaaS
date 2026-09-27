// Command institutions runs the institutions and credits service.
package main

import (
	"context"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/app"
	"clearsky/contracts/topology"

	"institutions_service/internal/institutions"
)

func main() {
	app.Main(app.Options{
		Service:    "institutions",
		Migrations: institutions.Migrations(),
		Setup: func(_ context.Context, env *app.Env) error {
			svc := &institutions.Service{DB: env.DB}
			env.Consume(amqpx.NewServer("institutions", topology.Institutions, 8, svc.Handle))
			return nil
		},
	})
}
