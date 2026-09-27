// Package app runs a ClearSky service the same way everywhere: logging and
// tracing, Postgres (with migrations), RabbitMQ, /health endpoints, and a
// graceful shutdown on SIGTERM. A failing background worker stops the
// process with a non-zero status so the container runtime restarts it.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/health"
	"clearsky/contracts/obs"
	"clearsky/contracts/pgdb"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Options describe one service.
type Options struct {
	Service    string
	Migrations fs.FS // *.sql goose migrations; nil when the service has no database
	HTTPAddr   string
	// Setup registers consumers, background loops and extra HTTP routes.
	Setup func(ctx context.Context, env *Env) error
}

// Env is what Setup receives.
type Env struct {
	DB      *pgxpool.Pool
	DBURL   string
	AMQP    *amqp.Connection
	Mux     *http.ServeMux // /health/* is already registered
	workers []func(context.Context) error
	checks  map[string]health.Check
}

// Go runs fn until ctx is cancelled; an error from fn stops the service.
func (e *Env) Go(fn func(ctx context.Context) error) { e.workers = append(e.workers, fn) }

// Consume serves an owned queue with the RPC server.
func (e *Env) Consume(server *amqpx.Server) {
	e.Go(func(ctx context.Context) error { return server.Serve(ctx, e.AMQP) })
}

// Check adds a readiness check.
func (e *Env) Check(name string, check health.Check) { e.checks[name] = check }

// Main runs the service and exits the process when it stops.
func Main(opts Options) {
	if err := Run(opts); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

// Run is Main without os.Exit (testable).
func Run(opts Options) error {
	ctx, stop := health.SignalContext()
	defer stop()

	shutdownTracing, err := obs.Setup(ctx, opts.Service)
	if err != nil {
		return fmt.Errorf("observability: %w", err)
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(flushCtx)
	}()

	env := &Env{Mux: http.NewServeMux(), checks: map[string]health.Check{}}
	if opts.Migrations != nil {
		env.DBURL = os.Getenv("DATABASE_URL")
		if env.DB, err = pgdb.Open(ctx, env.DBURL); err != nil {
			return err
		}
		defer env.DB.Close()
		if err := pgdb.Migrate(ctx, env.DBURL, opts.Migrations); err != nil {
			return err
		}
		env.Check("database", func(ctx context.Context) error { return env.DB.Ping(ctx) })
	}
	if url := os.Getenv("AMQP_URL"); url != "" {
		if env.AMQP, err = amqpx.Dial(ctx, url); err != nil {
			return err
		}
		defer env.AMQP.Close()
		env.Check("rabbitmq", func(context.Context) error {
			if env.AMQP.IsClosed() {
				return errors.New("connection closed")
			}
			return nil
		})
	}
	if opts.Setup != nil {
		if err := opts.Setup(ctx, env); err != nil {
			return err
		}
	}

	addr := opts.HTTPAddr
	if addr == "" {
		addr = ":8080"
	}
	healthHandler := health.Handler(env.checks)
	env.Mux.Handle("/health/", healthHandler)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(env.workers)+1)
	var wg sync.WaitGroup
	start := func(fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				errs <- err
			}
		}()
	}
	start(func(ctx context.Context) error { return health.Serve(ctx, addr, env.Mux) })
	for _, worker := range env.workers {
		start(worker)
	}
	slog.Info("service started", "http", addr)

	var runErr error
	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case runErr = <-errs:
	}
	cancel()
	wg.Wait()
	return runErr
}
