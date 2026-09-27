// Package google signs users in with Google (SRS: "Login with Google").
// Google proves email ownership; ClearSky decides the account and role.
package google

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"identity_service/internal/accounts"
	jwtutil "identity_service/pkg/jwt"

	"clearsky/contracts/rpc"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
)

const (
	stateCookie  = "oauth_state"
	signupCookie = "google_signup"
	jwtCookie    = "jwt"
	callbackPath = "/auth/google/callback"
)

// Identity is what Google says about the user.
type Identity struct {
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	HostedDomain  string `json:"hd"`
	Subject       string `json:"id"`
}

// Handlers serves /auth/google/login and /auth/google/callback.
type Handlers struct {
	Accounts *accounts.Service
	// FetchIdentity exchanges the code and reads the user info; replaceable in tests.
	FetchIdentity func(ctx context.Context, cfg *oauth2.Config, code string) (Identity, error)
}

func oauthConfig() (*oauth2.Config, error) {
	clientID, secret, redirect := os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET"), os.Getenv("GOOGLE_REDIRECT_URL")
	if clientID == "" || secret == "" || redirect == "" {
		return nil, errors.New("Google OAuth is not configured")
	}
	return &oauth2.Config{ClientID: clientID, ClientSecret: secret, RedirectURL: redirect,
		Scopes: []string{"openid", "email", "profile"}, Endpoint: googleoauth.Endpoint}, nil
}

func secureCookies() bool { return strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true") }

func frontendURL() string {
	if url := strings.TrimRight(os.Getenv("FRONTEND_URL"), "/"); url != "" {
		return url
	}
	return "http://localhost:3000"
}

func setCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, Secure: secureCookies(),
		SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

// redirectToLogin shows a fixed, non-sensitive reason on the login page.
func redirectToLogin(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, frontendURL()+"/login?error="+reason, http.StatusSeeOther)
}

// Register adds the routes to mux.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/google/login", h.Login)
	mux.HandleFunc("GET "+callbackPath, h.Callback)
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	cfg, err := oauthConfig()
	if err != nil {
		redirectToLogin(w, r, "google_unavailable")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "Google login is unavailable", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	var options []oauth2.AuthCodeOption
	if policy, err := FromEnvironment(); err == nil && policy.HostedDomainHint() != "" {
		options = append(options, oauth2.SetAuthURLParam("hd", policy.HostedDomainHint()))
	}
	setCookie(w, stateCookie, state, callbackPath, 600)
	http.Redirect(w, r, cfg.AuthCodeURL(state, options...), http.StatusSeeOther)
}

func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	cfg, err := oauthConfig()
	if err != nil {
		http.Error(w, "Google login is unavailable", http.StatusServiceUnavailable)
		return
	}
	stateCookieValue, err := r.Cookie(stateCookie)
	provided := r.URL.Query().Get("state")
	if err != nil || provided == "" || len(provided) != len(stateCookieValue.Value) ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(stateCookieValue.Value)) != 1 {
		http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
		return
	}
	setCookie(w, stateCookie, "", callbackPath, -1)
	code := r.URL.Query().Get("code")
	if code == "" {
		redirectToLogin(w, r, "google_login_failed")
		return
	}
	policy, err := FromEnvironment()
	if err != nil {
		slog.ErrorContext(r.Context(), "Google access policy", "error", err)
		http.Error(w, "Google login is unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	fetch := h.FetchIdentity
	if fetch == nil {
		fetch = fetchIdentity
	}
	identity, err := fetch(ctx, cfg, code)
	if err != nil {
		slog.WarnContext(ctx, "Google sign-in failed", "error", err)
		redirectToLogin(w, r, "google_login_failed")
		return
	}
	if identity.Email == "" || !identity.VerifiedEmail {
		redirectToLogin(w, r, "google_login_failed")
		return
	}
	if !policy.Allows(identity.Email, identity.HostedDomain) {
		redirectToLogin(w, r, "google_domain")
		return
	}
	result, err := h.Accounts.GoogleSignIn(identity.Email, identity.Subject)
	if err != nil {
		rpcErr, _ := rpc.AsError(err)
		switch {
		case rpcErr != nil && rpcErr.Code == rpc.CodeForbidden:
			redirectToLogin(w, r, "google_not_registered")
		case rpcErr != nil && rpcErr.Code == rpc.CodeConflict:
			redirectToLogin(w, r, "google_account_conflict")
		default:
			redirectToLogin(w, r, "google_login_failed")
		}
		return
	}
	if result.SignupTicket != "" {
		setCookie(w, signupCookie, result.SignupTicket, "/", 900)
		http.Redirect(w, r, frontendURL()+"/signup/google", http.StatusSeeOther)
		return
	}
	u := result.User
	token, err := jwtutil.GenerateToken(u.ID, u.InstitutionID, u.Username, u.Role, u.StudentID)
	if err != nil {
		slog.ErrorContext(ctx, "issue application token", "error", err)
		redirectToLogin(w, r, "google_login_failed")
		return
	}
	setCookie(w, jwtCookie, token, "/", 24*60*60)
	http.Redirect(w, r, frontendURL()+"/login/google/success", http.StatusSeeOther)
}

// fetchIdentity exchanges the authorisation code and reads Google's user info.
func fetchIdentity(ctx context.Context, cfg *oauth2.Config, code string) (Identity, error) {
	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("exchange code: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return Identity{}, err
	}
	resp, err := cfg.Client(ctx, token).Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("user info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("user info status %d", resp.StatusCode)
	}
	var identity Identity
	if err := json.NewDecoder(resp.Body).Decode(&identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}
