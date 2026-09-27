package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"identity_service/internal/accounts"
	"identity_service/internal/model"
	"identity_service/internal/store"

	"clearsky/contracts/ids"
	"clearsky/contracts/pgtest"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"gorm.io/gorm/logger"
)

func setup(t *testing.T, identity Identity) (*Handlers, *accounts.Service) {
	t.Helper()
	for k, v := range map[string]string{
		"GOOGLE_CLIENT_ID": "client", "GOOGLE_CLIENT_SECRET": "secret", "GOOGLE_REDIRECT_URL": "https://localhost/auth/google/callback",
		"GOOGLE_ALLOWED_DOMAINS": "uni.example", "GOOGLE_REQUIRE_WORKSPACE": "true", "FRONTEND_URL": "https://localhost",
		"JWT_SECRET": "test-signing-key-with-at-least-32-bytes", "JWT_ISSUER": "", "JWT_AUDIENCE": "",
	} {
		t.Setenv(k, v)
	}
	db, err := store.Open(pgtest.URL(t, store.Migrations()))
	if err != nil {
		t.Fatal(err)
	}
	db.Logger = logger.Discard
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	svc := &accounts.Service{DB: db}
	h := &Handlers{Accounts: svc, FetchIdentity: func(context.Context, *oauth2.Config, string) (Identity, error) { return identity, nil }}
	return h, svc
}

func callback(h *Handlers, state, cookieState string) *httptest.ResponseRecorder {
	q := url.Values{"state": {state}, "code": {"auth-code"}}
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?"+q.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: stateCookie, Value: cookieState})
	rec := httptest.NewRecorder()
	h.Callback(rec, req)
	return rec
}

func cookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestCallbackRejectsMismatchedState(t *testing.T) {
	h, _ := setup(t, Identity{Email: "a@uni.example", VerifiedEmail: true, HostedDomain: "uni.example"})
	rec := callback(h, "attacker", "expected")
	if rec.Code != http.StatusBadRequest || cookie(rec, jwtCookie) != nil {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestCallbackEnforcesTheDomainPolicy(t *testing.T) {
	h, _ := setup(t, Identity{Email: "alice@gmail.com", VerifiedEmail: true})
	rec := callback(h, "s", "s")
	if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, "/login?error=google_domain") || cookie(rec, jwtCookie) != nil {
		t.Fatalf("redirect = %q", loc)
	}
}

func TestCallbackSignsInExistingAccounts(t *testing.T) {
	h, svc := setup(t, Identity{Email: "prof@uni.example", VerifiedEmail: true, HostedDomain: "uni.example", Subject: "g-1"})
	prof := model.User{ID: uuid.NewString(), InstitutionID: ids.Institution("NTUA"), Username: "prof@uni.example", Role: "instructor"}
	if err := svc.DB.Create(&prof).Error; err != nil {
		t.Fatal(err)
	}
	rec := callback(h, "s", "s")
	session := cookie(rec, jwtCookie)
	if session == nil || !session.HttpOnly || rec.Header().Get("Location") != "https://localhost/login/google/success" {
		t.Fatalf("session = %+v, redirect %q", session, rec.Header().Get("Location"))
	}
}

func TestCallbackStartsSignupForRosterStudents(t *testing.T) {
	h, svc := setup(t, Identity{Email: "alice@uni.example", VerifiedEmail: true, HostedDomain: "uni.example", Subject: "g-2"})
	entry := model.StudentRosterEntry{InstitutionID: ids.Institution("NTUA"), StudentID: "03100001", Email: "alice@uni.example", UploadedBy: uuid.NewString()}
	if err := svc.DB.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	rec := callback(h, "s", "s")
	ticket := cookie(rec, signupCookie)
	if ticket == nil || ticket.Value == "" || cookie(rec, jwtCookie) != nil || rec.Header().Get("Location") != "https://localhost/signup/google" {
		t.Fatalf("ticket = %+v, redirect %q", ticket, rec.Header().Get("Location"))
	}
	if _, err := svc.CompleteGoogleSignup(ticket.Value, "03100001"); err != nil {
		t.Fatalf("the cookie's ticket must complete the signup: %v", err)
	}
}
