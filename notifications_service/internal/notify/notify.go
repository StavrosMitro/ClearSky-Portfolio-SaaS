// Package notify delivers emails from an outbox (roadmap 3.8): requests are
// stored first, then a sender delivers them within an hourly budget with
// retries, so a slow or failing SMTP relay never delays the requester.
package notify

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"clearsky/contracts/messages"
	"clearsky/contracts/rpc"

	notifymail "notifications_service/internal/mail"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

const (
	maxAttempts = 8
	leaseTime   = 5 * time.Minute
	batchSize   = 20
	maxBody     = 20000
)

type Service struct {
	DB          *pgxpool.Pool
	Mailer      notifymail.Sender // nil: requests are stored but not sent
	HourlyLimit int
	Now         func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

type payload struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Enqueue stores an email request; a repeated dedupe key is ignored.
func (s *Service) Enqueue(ctx context.Context, req messages.EmailRequest) (bool, error) {
	address, err := mail.ParseAddress(req.Recipient)
	if err != nil || address.Name != "" || address.Address != req.Recipient {
		return false, rpc.Fail(rpc.CodeInvalidRequest, "The recipient must be a plain email address")
	}
	if req.Subject == "" || strings.ContainsAny(req.Subject, "\r\n") || req.Body == "" || utf8.RuneCountInString(req.Body) > maxBody {
		return false, rpc.Fail(rpc.CodeInvalidRequest, "An email needs a one-line subject and a body")
	}
	template := req.Template
	if template == "" {
		template = "generic"
	}
	var institution, dedupe *string
	if req.InstitutionID != "" {
		institution = &req.InstitutionID
	}
	if req.DedupeKey != "" {
		dedupe = &req.DedupeKey
	}
	body, _ := json.Marshal(payload{Subject: req.Subject, Body: req.Body})
	tag, err := s.DB.Exec(ctx, `INSERT INTO email_outbox (institution_id, recipient, template, payload, dedupe_key, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (dedupe_key) DO NOTHING`, institution, req.Recipient, template, body, dedupe, s.now())
	if err != nil {
		slog.ErrorContext(ctx, "enqueue email", "error", err)
		return false, rpc.Unavailable("Email delivery is temporarily unavailable")
	}
	return tag.RowsAffected() == 1, nil
}

// HandleEmail consumes notifications.email messages.
func (s *Service) HandleEmail(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
	if msgType != messages.TypeEmail {
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown notification message")
	}
	var req messages.EmailRequest
	if err := rpc.Bind(body, &req); err != nil {
		return nil, err
	}
	stored, err := s.Enqueue(ctx, req)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"stored": stored}, nil
}

type leased struct {
	ID        string
	Recipient string
	Template  string
	Payload   payload
	Attempts  int
}

// budgetLeft is how many emails may still go out in the current hour.
func (s *Service) budgetLeft(ctx context.Context) (int, error) {
	if s.HourlyLimit <= 0 {
		return batchSize, nil
	}
	var sent int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE status = 'sent' AND sent_at > $1`,
		s.now().Add(-time.Hour)).Scan(&sent)
	return max(0, s.HourlyLimit-sent), err
}

// lease claims due emails for this sender only (safe with many replicas).
func (s *Service) lease(ctx context.Context, limit int) ([]leased, error) {
	rows, err := s.DB.Query(ctx, `UPDATE email_outbox SET next_attempt_at = $1
		WHERE id IN (SELECT id FROM email_outbox WHERE status = 'pending' AND next_attempt_at <= $2
			ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED)
		RETURNING id, recipient, template, payload, attempts`, s.now().Add(leaseTime), s.now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []leased
	for rows.Next() {
		var l leased
		var raw []byte
		if err := rows.Scan(&l.ID, &l.Recipient, &l.Template, &raw, &l.Attempts); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &l.Payload)
		list = append(list, l)
	}
	return list, rows.Err()
}

// SendDue delivers due emails within the hourly budget. It returns how many
// were sent.
func (s *Service) SendDue(ctx context.Context) (int, error) {
	if s.Mailer == nil {
		return 0, nil
	}
	left, err := s.budgetLeft(ctx)
	if err != nil || left == 0 {
		return 0, err
	}
	batch, err := s.lease(ctx, min(left, batchSize))
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, email := range batch {
		sendErr := s.Mailer.Send(ctx, notifymail.Message{To: email.Recipient, Subject: email.Payload.Subject, Body: email.Payload.Body})
		if sendErr == nil {
			sent++
			_, err = s.DB.Exec(ctx, `UPDATE email_outbox SET status = 'sent', sent_at = $2, payload = NULL, last_error = NULL,
				attempts = attempts + 1 WHERE id = $1`, email.ID, s.now())
		} else {
			attempts := email.Attempts + 1
			slog.WarnContext(ctx, "email delivery failed", "template", email.Template, "attempt", attempts, "error", sendErr)
			if attempts >= maxAttempts {
				_, err = s.DB.Exec(ctx, `UPDATE email_outbox SET status = 'failed', payload = NULL, attempts = $2, last_error = $3
					WHERE id = $1`, email.ID, attempts, truncate(sendErr.Error()))
			} else {
				_, err = s.DB.Exec(ctx, `UPDATE email_outbox SET attempts = $2, last_error = $3, next_attempt_at = $4 WHERE id = $1`,
					email.ID, attempts, truncate(sendErr.Error()), s.now().Add(backoff(attempts)))
			}
		}
		if err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// backoff: 30s, 1m, 2m, ... capped at one hour.
func backoff(attempts int) time.Duration {
	delay := 30 * time.Second << (attempts - 1)
	if delay > time.Hour || delay <= 0 {
		return time.Hour
	}
	return delay
}

func truncate(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// SendLoop delivers due emails every second until ctx ends.
func (s *Service) SendLoop(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.SendDue(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "email sender", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Stats counts outbox rows by status (for tests and diagnostics).
func (s *Service) Stats(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT status, count(*) FROM email_outbox GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}
