// Package health serves /health/live and /health/ready and ties the process
// lifetime to SIGINT/SIGTERM (roadmap 2.3).
package health

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Check reports whether one dependency is usable.
type Check func(ctx context.Context) error

// Handler returns the health endpoints. /health/live answers 200 while the
// process runs; /health/ready answers 200 only when every check passes.
func Handler(checks map[string]Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "live"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		failures := map[string]string{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name, check := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := check(ctx); err != nil {
					mu.Lock()
					failures[name] = err.Error()
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if len(failures) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "failures": failures})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// SignalContext is cancelled on SIGINT or SIGTERM (docker stop).
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// Serve runs handler on addr until ctx is cancelled, then drains in-flight
// requests for up to 10 seconds.
func Serve(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http shutdown", "error", err)
		}
		return nil
	}
}
