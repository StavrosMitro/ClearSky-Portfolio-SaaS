package query

import (
	"context"
	"log/slog"
	"time"

	"clearsky/contracts/messages"
)

// Source is grades-ingest, the source of truth.
type Source interface {
	Headers(ctx context.Context) ([]messages.GradingHeader, error)
	Snapshot(ctx context.Context, gradingID string) (messages.GradingSnapshot, error)
}

// Reconcile pulls every grading whose version is newer in grades-ingest.
// It repairs a lost synchronisation message and rebuilds an empty store.
func (s *Service) Reconcile(ctx context.Context, source Source) (int, error) {
	headers, err := source.Headers(ctx)
	if err != nil {
		return 0, err
	}
	stored, err := s.Versions(ctx)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, h := range headers {
		if stored[h.GradingID] >= h.Version {
			continue
		}
		snap, err := source.Snapshot(ctx, h.GradingID)
		if err != nil {
			return applied, err
		}
		changed, err := s.Apply(ctx, snap)
		if err != nil {
			return applied, err
		}
		if changed {
			applied++
		}
	}
	return applied, nil
}

// ReconcileLoop reconciles at start and then every interval until ctx ends.
// Failures are logged and retried next time; they never stop the service.
func (s *Service) ReconcileLoop(ctx context.Context, source Source, interval time.Duration) error {
	for {
		if applied, err := s.Reconcile(ctx, source); err != nil {
			slog.WarnContext(ctx, "reconcile failed", "error", err)
		} else if applied > 0 {
			slog.InfoContext(ctx, "reconcile applied missed updates", "gradings", applied)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
