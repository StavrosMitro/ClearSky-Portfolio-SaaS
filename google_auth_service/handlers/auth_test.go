package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const testInternalToken = "internal-token-with-at-least-32-bytes"

func TestExchangeVerifiedEmailSendsOnlyEmailWithInternalToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/google-login" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testInternalToken {
			t.Errorf("Authorization header = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if len(body) != 1 || body["email"] != "teacher@example.com" {
			t.Errorf("body = %v, want only the verified email", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "application-token", "role": "instructor", "user_id": "user-1"})
	}))
	defer server.Close()
	t.Setenv("UMS_URL", server.URL)
	t.Setenv("INTERNAL_AUTH_TOKEN", testInternalToken)

	login, err := exchangeVerifiedEmail(context.Background(), "teacher@example.com")
	if err != nil {
		t.Fatalf("exchangeVerifiedEmail: %v", err)
	}
	if login.Token != "application-token" || login.Role != "instructor" || login.UserID != "user-1" {
		t.Fatalf("login = %+v, want the user-management identity unchanged", login)
	}
}

func TestExchangeVerifiedEmailRejectsFailedOrIncompleteReplies(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"not provisioned": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		"missing token": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"role": "student", "user_id": "user-1"})
		},
		"malformed": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			t.Setenv("UMS_URL", server.URL)
			t.Setenv("INTERNAL_AUTH_TOKEN", testInternalToken)
			if _, err := exchangeVerifiedEmail(context.Background(), "teacher@example.com"); err == nil {
				t.Fatal("expected the exchange to fail")
			}
		})
	}
}

func TestGoogleCallbackRejectsMismatchedState(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8086/auth/google/callback")

	query := url.Values{"state": {"attacker-state"}, "code": {"code"}}
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?"+query.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "expected-state"})
	rec := httptest.NewRecorder()
	GoogleCallbackHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "jwt" {
			t.Fatal("mismatched OAuth state must not set an application session")
		}
	}
}

func TestExchangeVerifiedEmailReturnsSignupTicketForRosterStudent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"signup_ticket": "ticket-1"})
	}))
	defer server.Close()
	t.Setenv("UMS_URL", server.URL)
	t.Setenv("INTERNAL_AUTH_TOKEN", testInternalToken)

	login, err := exchangeVerifiedEmail(context.Background(), "alice@uni.example")
	if err != nil || login.SignupTicket != "ticket-1" || login.Token != "" {
		t.Fatalf("login = %+v, err = %v", login, err)
	}
}

func TestExchangeVerifiedEmailReportsStatusForLoginMessage(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusConflict} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		t.Setenv("UMS_URL", server.URL)
		t.Setenv("INTERNAL_AUTH_TOKEN", testInternalToken)
		_, err := exchangeVerifiedEmail(context.Background(), "alice@uni.example")
		server.Close()
		var statusErr *exchangeStatusError
		if !errors.As(err, &statusErr) || statusErr.status != status {
			t.Fatalf("status %d: err = %v", status, err)
		}
	}
}

func TestGoogleLoginRequestsTheUniversityAccount(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8086/auth/google/callback")
	t.Setenv("GOOGLE_ALLOWED_DOMAINS", "dept.uni.gr")
	rec := httptest.NewRecorder()
	GoogleLoginHandler(rec, httptest.NewRequest(http.MethodGet, "/auth/google/login", nil))
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || location.Query().Get("hd") != "dept.uni.gr" || location.Query().Get("state") == "" {
		t.Fatalf("redirect = %q, %v", rec.Header().Get("Location"), err)
	}
}
