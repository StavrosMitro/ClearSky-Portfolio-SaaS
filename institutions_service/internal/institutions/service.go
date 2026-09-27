// Package institutions owns institutions and their credits (SRS 2.2, 2.4).
// One credit pays for one course grading (initial + final upload).
package institutions

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"clearsky/contracts/rpc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the goose migrations of this service.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

const maxPurchase = 10000

type Service struct{ DB *pgxpool.Pool }

type Institution struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	ContactEmail string    `json:"contact_email"`
	Director     string    `json:"director,omitempty"`
	Status       string    `json:"status"`
	Credits      int       `json:"credits"`
	CreatedAt    time.Time `json:"created_at"`
}

type Summary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Balance struct {
	InstitutionID string `json:"institution_id"`
	Credits       int    `json:"credits"`
	Applied       bool   `json:"applied"` // false when the idempotency key was already used
}

type LedgerEntry struct {
	ID        string    `json:"id"`
	Delta     int       `json:"delta"`
	Reason    string    `json:"reason"`
	Reference string    `json:"reference,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func storeUnavailable(ctx context.Context, err error) error {
	slog.ErrorContext(ctx, "institutions store", "error", err)
	return rpc.Unavailable("The institutions store is unavailable")
}

func errNotRegistered() error {
	return rpc.Fail(rpc.CodeNotFound, "The institution is not registered yet")
}

func validID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

// Register creates the institution (id = the representative's institution)
// or updates its details.
func (s *Service) Register(ctx context.Context, id, name, contactEmail, director string) (Institution, error) {
	name, director = strings.Join(strings.Fields(name), " "), strings.TrimSpace(director)
	contactEmail = strings.ToLower(strings.TrimSpace(contactEmail))
	if !validID(id) {
		return Institution{}, rpc.Fail(rpc.CodeInvalidRequest, "A valid institution is required")
	}
	if len([]rune(name)) < 2 || len([]rune(name)) > 100 {
		return Institution{}, rpc.Fail(rpc.CodeInvalidRequest, "The institution name must contain 2 to 100 characters")
	}
	if addr, err := mail.ParseAddress(contactEmail); err != nil || addr.Address != contactEmail {
		return Institution{}, rpc.Fail(rpc.CodeInvalidRequest, "A valid contact email is required")
	}
	if len([]rune(director)) > 100 {
		return Institution{}, rpc.Fail(rpc.CodeInvalidRequest, "The director's name must contain at most 100 characters")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Institution{}, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO institutions (id, name, contact_email, director) VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, contact_email = EXCLUDED.contact_email,
			director = EXCLUDED.director, updated_at = now()`, id, name, contactEmail, director)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Institution{}, rpc.Fail(rpc.CodeConflict, "An institution with this name is already registered")
	}
	if err != nil {
		return Institution{}, storeUnavailable(ctx, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credit_balances (institution_id, balance) VALUES ($1, 0) ON CONFLICT DO NOTHING`, id); err != nil {
		return Institution{}, storeUnavailable(ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Institution{}, storeUnavailable(ctx, err)
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (Institution, error) {
	if !validID(id) {
		return Institution{}, rpc.Fail(rpc.CodeInvalidRequest, "A valid institution is required")
	}
	var inst Institution
	var director *string
	err := s.DB.QueryRow(ctx, `
		SELECT i.id, i.name, i.contact_email, i.director, i.status, b.balance, i.created_at
		FROM institutions i JOIN credit_balances b ON b.institution_id = i.id WHERE i.id = $1`, id).
		Scan(&inst.ID, &inst.Name, &inst.ContactEmail, &director, &inst.Status, &inst.Credits, &inst.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Institution{}, errNotRegistered()
	}
	if err != nil {
		return Institution{}, storeUnavailable(ctx, err)
	}
	if director != nil {
		inst.Director = *director
	}
	return inst, nil
}

// List returns the active institutions (public: names only).
func (s *Service) List(ctx context.Context) ([]Summary, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name FROM institutions WHERE status = 'active' ORDER BY lower(name)`)
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Summary])
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	return list, nil
}

// Purchase adds credits. A repeated idempotency key adds nothing.
func (s *Service) Purchase(ctx context.Context, id string, amount int, idempotencyKey, actor string) (Balance, error) {
	if amount < 1 || amount > maxPurchase {
		return Balance{}, rpc.Fail(rpc.CodeInvalidRequest, "Buy between 1 and 10000 credits at a time")
	}
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	return s.move(ctx, id, amount, "purchase", "", "purchase:"+idempotencyKey, actor)
}

// Charge takes one credit for a grading, at most once per grading.
func (s *Service) Charge(ctx context.Context, id, gradingID, actor string) (Balance, error) {
	if !validID(gradingID) {
		return Balance{}, rpc.Fail(rpc.CodeInvalidRequest, "A valid grading is required")
	}
	return s.move(ctx, id, -1, "grading_charge", gradingID, "charge:"+gradingID, actor)
}

func (s *Service) move(ctx context.Context, id string, delta int, reason, reference, key, actor string) (Balance, error) {
	if !validID(id) {
		return Balance{}, rpc.Fail(rpc.CodeInvalidRequest, "A valid institution is required")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Balance{}, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	// The row lock serialises movements of one institution.
	var balance int
	err = tx.QueryRow(ctx, `SELECT balance FROM credit_balances WHERE institution_id = $1 FOR UPDATE`, id).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return Balance{}, errNotRegistered()
	}
	if err != nil {
		return Balance{}, storeUnavailable(ctx, err)
	}
	var seen bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM credit_ledger WHERE idempotency_key = $1)`, key).Scan(&seen); err != nil {
		return Balance{}, storeUnavailable(ctx, err)
	}
	if seen {
		return Balance{InstitutionID: id, Credits: balance}, nil
	}
	if balance+delta < 0 {
		return Balance{}, rpc.Fail(rpc.CodeInsufficientCredits, "Not enough credits; buy more credits to publish this grading")
	}
	var actorID *string
	if validID(actor) {
		actorID = &actor
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger (institution_id, delta, reason, reference, idempotency_key, created_by)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)`, id, delta, reason, reference, key, actorID); err != nil {
		return Balance{}, storeUnavailable(ctx, err)
	}
	if err := tx.QueryRow(ctx, `UPDATE credit_balances SET balance = balance + $2, updated_at = now()
		WHERE institution_id = $1 RETURNING balance`, id, delta).Scan(&balance); err != nil {
		return Balance{}, storeUnavailable(ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Balance{}, storeUnavailable(ctx, err)
	}
	return Balance{InstitutionID: id, Credits: balance, Applied: true}, nil
}

// History lists the most recent credit movements.
func (s *Service) History(ctx context.Context, id string, limit int) ([]LedgerEntry, error) {
	if !validID(id) {
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "A valid institution is required")
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT id, delta, reason, COALESCE(reference, ''), created_at FROM credit_ledger
		WHERE institution_id = $1 ORDER BY created_at DESC, id LIMIT $2`, id, limit)
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[LedgerEntry])
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	return entries, nil
}
