// Command identity runs accounts, sign-in (password and Google) and
// onboarding. It is the only issuer of application JWTs.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"identity_service/internal/accounts"
	"identity_service/internal/config"
	"identity_service/internal/google"
	"identity_service/internal/messaging"
	"identity_service/internal/notifier"
	"identity_service/internal/store"
	jwtutil "identity_service/pkg/jwt"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/app"
	"clearsky/contracts/topology"
)

func main() {
	app.Main(app.Options{
		Service:    "identity",
		Migrations: store.Migrations(),
		Setup: func(_ context.Context, env *app.Env) error {
			if err := jwtutil.ValidateConfiguration(); err != nil {
				return fmt.Errorf("JWT configuration: %w", err)
			}
			if os.Getenv("GOOGLE_CLIENT_ID") != "" {
				if _, err := google.FromEnvironment(); err != nil {
					return fmt.Errorf("Google access policy: %w", err)
				}
			}
			db, err := store.Open(env.DBURL)
			if err != nil {
				return err
			}
			if err := config.Bootstrap(db); err != nil {
				return err
			}
			pub, err := amqpx.NewPublisher(env.AMQP)
			if err != nil {
				return err
			}
			appURL := strings.TrimSpace(os.Getenv("PUBLIC_APP_URL"))
			if appURL == "" {
				appURL = "http://localhost:3000"
			}
			svc := &accounts.Service{DB: db, Mailer: notifier.Publisher{Pub: pub}, AppURL: appURL}
			env.Consume(amqpx.NewServer("identity", topology.Identity, 8, messaging.Handler(svc)))
			(&google.Handlers{Accounts: svc}).Register(env.Mux)
			return nil
		},
	})
}
