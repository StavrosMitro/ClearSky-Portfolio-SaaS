package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"clearsky/contracts/messages"
	"clearsky/contracts/pgtest"
	"clearsky/contracts/rpc"

	"notifications_service/internal/mail"
)

type fakeMailer struct {
	sent []mail.Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

type env struct {
	svc    *Service
	mailer *fakeMailer
	clock  time.Time
}

func newEnv(t *testing.T, limit int) *env {
	t.Helper()
	e := &env{mailer: &fakeMailer{}, clock: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	e.svc = &Service{DB: pgtest.Pool(t, Migrations()), Mailer: e.mailer, HourlyLimit: limit, Now: func() time.Time { return e.clock }}
	return e
}

func email(to, key string) messages.EmailRequest {
	return messages.EmailRequest{Type: messages.TypeEmail, Recipient: to, DedupeKey: key, Template: "student_activation",
		Subject: "Confirm your ClearSky account", Body: "Open https://clearsky.example/activate#token=secret"}
}

func TestEnqueueDeduplicatesAndValidates(t *testing.T) {
	e := newEnv(t, 100)
	ctx := context.Background()
	if stored, err := e.svc.Enqueue(ctx, email("alice@uni.example", "k1")); err != nil || !stored {
		t.Fatalf("enqueue: %v %v", stored, err)
	}
	if stored, _ := e.svc.Enqueue(ctx, email("alice@uni.example", "k1")); stored {
		t.Fatal("a repeated dedupe key must be ignored")
	}
	for _, bad := range []messages.EmailRequest{
		email("Mallory <m@evil.example>", "k2"),
		{Recipient: "a@b.example", Subject: "two\r\nlines", Body: "x"},
		{Recipient: "a@b.example", Subject: "no body"},
	} {
		_, err := e.svc.Enqueue(ctx, bad)
		if rpcErr, ok := rpc.AsError(err); !ok || rpcErr.Code != rpc.CodeInvalidRequest {
			t.Fatalf("invalid request %+v accepted: %v", bad, err)
		}
	}
}

func TestSendDueDeliversAndForgetsContent(t *testing.T) {
	e := newEnv(t, 100)
	ctx := context.Background()
	if _, err := e.svc.Enqueue(ctx, email("alice@uni.example", "k1")); err != nil {
		t.Fatal(err)
	}
	sent, err := e.svc.SendDue(ctx)
	if err != nil || sent != 1 || len(e.mailer.sent) != 1 || e.mailer.sent[0].To != "alice@uni.example" {
		t.Fatalf("sent = %d, %v, %+v", sent, err, e.mailer.sent)
	}
	var payload *string
	if err := e.svc.DB.QueryRow(ctx, `SELECT payload::text FROM email_outbox`).Scan(&payload); err != nil || payload != nil {
		t.Fatalf("the payload (with its link) must be deleted after sending: %v %v", payload, err)
	}
	if again, _ := e.svc.SendDue(ctx); again != 0 {
		t.Fatal("a sent email must not be sent again")
	}
}

func TestSendDueRespectsHourlyBudget(t *testing.T) {
	e := newEnv(t, 2)
	ctx := context.Background()
	for _, key := range []string{"a", "b", "c"} {
		if _, err := e.svc.Enqueue(ctx, email(key+"@uni.example", key)); err != nil {
			t.Fatal(err)
		}
	}
	if sent, _ := e.svc.SendDue(ctx); sent != 2 {
		t.Fatalf("sent %d with a budget of 2", sent)
	}
	if sent, _ := e.svc.SendDue(ctx); sent != 0 {
		t.Fatal("the third email must wait for the next hour")
	}
	e.clock = e.clock.Add(time.Hour + time.Minute)
	if sent, _ := e.svc.SendDue(ctx); sent != 1 {
		t.Fatal("the deferred email must go out once the budget refills")
	}
}

func TestFailuresRetryWithBackoffThenGiveUp(t *testing.T) {
	e := newEnv(t, 100)
	ctx := context.Background()
	if _, err := e.svc.Enqueue(ctx, email("alice@uni.example", "k")); err != nil {
		t.Fatal(err)
	}
	e.mailer.err = errors.New("relay down")
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if _, err := e.svc.SendDue(ctx); err != nil {
			t.Fatal(err)
		}
		if attempt < maxAttempts {
			if again, _ := e.svc.SendDue(ctx); again != 0 {
				t.Fatal("a failed email must wait for its backoff")
			}
		}
		e.clock = e.clock.Add(time.Hour + time.Second)
	}
	stats, _ := e.svc.Stats(ctx)
	if stats["failed"] != 1 || stats["pending"] != 0 {
		t.Fatalf("after %d failures: %v", maxAttempts, stats)
	}
	if backoff(1) != 30*time.Second || backoff(20) != time.Hour {
		t.Fatalf("backoff = %v / %v", backoff(1), backoff(20))
	}
}

func TestLeaseIsExclusive(t *testing.T) {
	e := newEnv(t, 100)
	ctx := context.Background()
	for _, key := range []string{"a", "b", "c"} {
		if _, err := e.svc.Enqueue(ctx, email(key+"@uni.example", key)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := e.svc.lease(ctx, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first lease = %d, %v", len(first), err)
	}
	second, _ := e.svc.lease(ctx, 10)
	if len(second) != 1 || second[0].ID == first[0].ID || second[0].ID == first[1].ID {
		t.Fatalf("a second sender must only get the unleased email: %+v", second)
	}
}
