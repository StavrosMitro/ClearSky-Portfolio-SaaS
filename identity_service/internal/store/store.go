// Package store opens the identity database (Postgres through GORM, with
// goose migrations owning the schema).
package store

import (
	"embed"
	"io/fs"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/opentelemetry/tracing"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the goose migrations.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

// Open connects GORM to an already migrated database.
func Open(url string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{Logger: logger.New(log.Default(), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		// Never write usernames or password hashes into logs.
		ParameterizedQueries: true,
	})})
	if err != nil {
		return nil, err
	}
	// Spans without SQL parameters (no personal data in traces either).
	if err := db.Use(tracing.NewPlugin(tracing.WithoutQueryVariables(), tracing.WithoutMetrics())); err != nil {
		return nil, err
	}
	return db, nil
}
