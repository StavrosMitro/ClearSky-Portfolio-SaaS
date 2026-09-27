// Command notifications delivers emails from its outbox.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/app"
	"clearsky/contracts/topology"

	"notifications_service/internal/mail"
	"notifications_service/internal/notify"
)

func main() {
	app.Main(app.Options{
		Service:    "notifications",
		Migrations: notify.Migrations(),
		Setup: func(_ context.Context, env *app.Env) error {
			sender, err := mail.SMTPFromEnvironment()
			if err != nil {
				return fmt.Errorf("SMTP configuration: %w", err)
			}
			limit := 500
			if raw := os.Getenv("EMAIL_HOURLY_LIMIT"); raw != "" {
				if limit, err = strconv.Atoi(raw); err != nil || limit < 1 {
					return fmt.Errorf("EMAIL_HOURLY_LIMIT must be a positive integer")
				}
			}
			svc := &notify.Service{DB: env.DB, HourlyLimit: limit}
			if sender != nil {
				svc.Mailer = sender
			} else {
				slog.Warn("SMTP_HOST is not set: emails are stored but not sent")
			}
			env.Consume(amqpx.NewServer("notifications", topology.Notifications, 4, svc.HandleEmail))
			env.Go(svc.SendLoop)
			return nil
		},
	})
}
