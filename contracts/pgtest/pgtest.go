// Package pgtest gives each test a fresh, migrated Postgres database.
//
// Tests need TEST_DATABASE_URL pointing at a server where the user may
// create databases (CI provides one; locally e.g. a postgres container).
// Without it the tests are skipped, never silently passed as green.
package pgtest

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"clearsky/contracts/pgdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// URL creates a database, applies migrations and returns its URL. The
// database is dropped when the test ends.
func URL(t testing.TB, migrations fs.FS) string {
	t.Helper()
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping Postgres test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if conn, err := pgx.Connect(ctx, admin); err == nil {
			_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = conn.Close(ctx)
		}
	})
	parsed, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	dbURL := parsed.String()
	if migrations != nil {
		if err := pgdb.Migrate(ctx, dbURL, migrations); err != nil {
			t.Fatalf("migrate test database: %v", err)
		}
	}
	return dbURL
}

// Pool returns a pool to a fresh migrated database.
func Pool(t testing.TB, migrations fs.FS) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), URL(t, migrations))
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Exec runs SQL on the pool and fails the test on error.
func Exec(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(fmt.Errorf("exec %q: %w", sql, err))
	}
}
