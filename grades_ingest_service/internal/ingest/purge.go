package ingest

import (
	"context"
	"log/slog"
	"time"
)

// PurgeUnpublished deletes uploads that were never published (cancelled,
// rejected, or previews left to expire). They hold a workbook with names and
// emails that nobody needs; published uploads are kept as the record of what
// was graded.
func (s *Service) PurgeUnpublished(ctx context.Context) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM uploads WHERE status <> 'confirmed' AND expires_at < $1`, s.now())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PurgeLoop runs PurgeUnpublished every interval until ctx ends.
func (s *Service) PurgeLoop(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if n, err := s.PurgeUnpublished(ctx); err != nil {
			slog.WarnContext(ctx, "purging unpublished uploads failed", "error", err)
		} else if n > 0 {
			slog.InfoContext(ctx, "purged unpublished uploads", "count", n)
		}
	}
}
