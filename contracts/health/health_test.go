package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadyReflectsChecks(t *testing.T) {
	failing := true
	h := Handler(map[string]Check{
		"database": func(context.Context) error {
			if failing {
				return errors.New("down")
			}
			return nil
		},
	})
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/health/live"); rec.Code != http.StatusOK {
		t.Fatalf("live = %d", rec.Code)
	}
	if rec := get("/health/ready"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "database") {
		t.Fatalf("ready with failing check = %d %s", rec.Code, rec.Body.String())
	}
	failing = false
	if rec := get("/health/ready"); rec.Code != http.StatusOK {
		t.Fatalf("ready = %d", rec.Code)
	}
}
