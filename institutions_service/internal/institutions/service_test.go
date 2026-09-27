package institutions

import (
	"context"
	"sync"
	"testing"

	"clearsky/contracts/ids"
	"clearsky/contracts/pgtest"
	"clearsky/contracts/rpc"

	"github.com/google/uuid"
)

func newService(t *testing.T) *Service {
	t.Helper()
	return &Service{DB: pgtest.Pool(t, Migrations())}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	rpcErr, ok := rpc.AsError(err)
	if !ok || rpcErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func register(t *testing.T, s *Service, name string) string {
	t.Helper()
	id := ids.Institution(name)
	if _, err := s.Register(context.Background(), id, name, "office@"+uuid.NewString()[:6]+".example", "Director"); err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return id
}

func TestRegisterCreatesThenUpdates(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	id := ids.Institution("NTUA")

	inst, err := s.Register(ctx, id, "  National   Technical University  ", "Office@NTUA.example", "A. Director")
	if err != nil {
		t.Fatal(err)
	}
	if inst.Name != "National Technical University" || inst.ContactEmail != "office@ntua.example" || inst.Credits != 0 {
		t.Fatalf("registered = %+v", inst)
	}
	inst, err = s.Register(ctx, id, "NTUA", "office@ntua.example", "")
	if err != nil || inst.Name != "NTUA" || inst.Director != "" {
		t.Fatalf("update = %+v, %v", inst, err)
	}

	_, err = s.Register(ctx, ids.Institution("Other"), "ntua", "x@y.example", "")
	assertCode(t, err, rpc.CodeConflict)
	for _, bad := range [][3]string{{"not-a-uuid", "Name", "a@b.example"}, {id, "N", "a@b.example"}, {id, "Name", "not-an-email"}} {
		_, err := s.Register(ctx, bad[0], bad[1], bad[2], "")
		assertCode(t, err, rpc.CodeInvalidRequest)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "NTUA" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestPurchaseAndChargeRequireRegistration(t *testing.T) {
	s := newService(t)
	ghost := ids.Institution("Ghost")
	_, err := s.Purchase(context.Background(), ghost, 5, "", "")
	assertCode(t, err, rpc.CodeNotFound)
	_, err = s.Charge(context.Background(), ghost, uuid.NewString(), "")
	assertCode(t, err, rpc.CodeNotFound)
	_, err = s.Get(context.Background(), ghost)
	assertCode(t, err, rpc.CodeNotFound)
}

func TestPurchaseIsIdempotentAndBounded(t *testing.T) {
	s := newService(t)
	id := register(t, s, "NTUA")
	ctx := context.Background()

	balance, err := s.Purchase(ctx, id, 10, "req-1", uuid.NewString())
	if err != nil || balance.Credits != 10 || !balance.Applied {
		t.Fatalf("purchase = %+v, %v", balance, err)
	}
	balance, err = s.Purchase(ctx, id, 10, "req-1", "")
	if err != nil || balance.Credits != 10 || balance.Applied {
		t.Fatalf("a repeated purchase must not add credits: %+v, %v", balance, err)
	}
	for _, amount := range []int{0, -3, maxPurchase + 1} {
		_, err := s.Purchase(ctx, id, amount, "", "")
		assertCode(t, err, rpc.CodeInvalidRequest)
	}
}

func TestChargeOncePerGradingAndNeverNegative(t *testing.T) {
	s := newService(t)
	id := register(t, s, "NTUA")
	ctx := context.Background()
	if _, err := s.Purchase(ctx, id, 2, "", ""); err != nil {
		t.Fatal(err)
	}
	grading := ids.Grading(id, "3205", "2024-2025 ΧΕΙΜ 2024")

	first, err := s.Charge(ctx, id, grading, "")
	if err != nil || first.Credits != 1 || !first.Applied {
		t.Fatalf("first charge = %+v, %v", first, err)
	}
	again, err := s.Charge(ctx, id, grading, "")
	if err != nil || again.Credits != 1 || again.Applied {
		t.Fatalf("the same grading must be charged once: %+v, %v", again, err)
	}
	if _, err := s.Charge(ctx, id, uuid.NewString(), ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.Charge(ctx, id, uuid.NewString(), "")
	assertCode(t, err, rpc.CodeInsufficientCredits)

	history, err := s.History(ctx, id, 10)
	if err != nil || len(history) != 3 {
		t.Fatalf("history = %+v, %v", history, err)
	}
	if history[0].Delta != -1 || history[len(history)-1].Delta != 2 || history[len(history)-1].Reason != "purchase" {
		t.Fatalf("history must list newest first: %+v", history)
	}
}

func TestConcurrentChargesCannotOverspend(t *testing.T) {
	s := newService(t)
	id := register(t, s, "NTUA")
	ctx := context.Background()
	if _, err := s.Purchase(ctx, id, 5, "", ""); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, insufficient := 0, 0
	sameGrading := uuid.NewString()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			grading := uuid.NewString()
			if i%4 == 0 {
				grading = sameGrading // five goroutines race on one grading
			}
			balance, err := s.Charge(ctx, id, grading, "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && balance.Applied:
				succeeded++
			case err != nil:
				if rpcErr, ok := rpc.AsError(err); ok && rpcErr.Code == rpc.CodeInsufficientCredits {
					insufficient++
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	final, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if succeeded != 5 || final.Credits != 0 {
		t.Fatalf("succeeded=%d insufficient=%d final=%d; want exactly 5 charges and 0 left", succeeded, insufficient, final.Credits)
	}
}
