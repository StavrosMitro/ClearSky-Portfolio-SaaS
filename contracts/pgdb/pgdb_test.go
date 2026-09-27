package pgdb_test

import (
	"context"
	"testing"
	"testing/fstest"

	"clearsky/contracts/pgdb"
	"clearsky/contracts/pgtest"
)

func TestMigrateIsIdempotent(t *testing.T) {
	migrations := fstest.MapFS{
		"00001_widgets.sql": {Data: []byte("-- +goose Up\nCREATE TABLE widgets (id int PRIMARY KEY);\n-- +goose Down\nDROP TABLE widgets;\n")},
	}
	url := pgtest.URL(t, migrations)
	if err := pgdb.Migrate(context.Background(), url, migrations); err != nil {
		t.Fatalf("second run must be a no-op: %v", err)
	}
	pool, err := pgdb.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), "INSERT INTO widgets VALUES (1)"); err != nil {
		t.Fatalf("migrated table missing: %v", err)
	}
}
