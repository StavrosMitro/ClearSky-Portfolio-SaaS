package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"google_auth_service/policy"
	"google_auth_service/rabbitmq"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// signupCookie carries user management's short-lived ticket to the
// student-ID step of a first Google login. It is HttpOnly and single-use.
const signupCookie = "google_signup"

type userManagementLogin struct {
	Token        string `json:"token"`
	Role         string `json:"role"`
	UserID       string `json:"user_id"`
	SignupTicket string `json:"signup_ticket"`
}

// exchangeStatusError carries user management's HTTP status so the callback
// can tell the user why sign-in failed.
type exchangeStatusError struct{ status int }

func (e *exchangeStatusError) Error() string {
	return fmt.Sprintf("user management status %d", e.status)
}

func frontendURL() string {
	if url := os.Getenv("FRONTEND_URL"); url != "" {
		return strings.TrimRight(url, "/")
	}
	return "http://localhost:3000"
}

// redirectToLogin shows a fixed, non-sensitive reason on the login page.
func redirectToLogin(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, frontendURL()+"/login?error="+reason, http.StatusTemporaryRedirect)
}

func oauthConfiguration() (*oauth2.Config, error) {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	redirectURL := os.Getenv("GOOGLE_REDIRECT_URL")
	if clientID == "" || clientSecret == "" || redirectURL == "" {
		return nil, errors.New("Google OAuth is not configured")
	}
	return &oauth2.Config{
		RedirectURL:  redirectURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}, nil
}

func secureCookie() bool { return strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true") }

func newOAuthState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func GoogleLoginHandler(w http.ResponseWriter, r *http.Request) {
	config, err := oauthConfiguration()
	if err != nil {
		http.Error(w, "Google login is unavailable", http.StatusServiceUnavailable)
		return
	}
	state, err := newOAuthState()
	if err != nil {
		http.Error(w, "Google login is unavailable", http.StatusInternalServerError)
		return
	}
	var options []oauth2.AuthCodeOption
	if accessPolicy, err := policy.FromEnvironment(); err == nil && accessPolicy.HostedDomainHint() != "" {
		options = append(options, oauth2.SetAuthURLParam("hd", accessPolicy.HostedDomainHint()))
	}
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, Path: "/auth/google/callback", HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, config.AuthCodeURL(state, options...), http.StatusTemporaryRedirect)
}

func GoogleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	config, err := oauthConfiguration()
	if err != nil {
		http.Error(w, "Google login is unavailable", http.StatusServiceUnavailable)
		return
	}
	stateCookie, err := r.Cookie("oauth_state")
	providedState := r.URL.Query().Get("state")
	if err != nil || providedState == "" || len(providedState) != len(stateCookie.Value) || subtle.ConstantTimeCompare([]byte(providedState), []byte(stateCookie.Value)) != 1 {
		http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: "", Path: "/auth/google/callback", HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Google did not provide an authorization code", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	token, err := config.Exchange(ctx, code)
	if err != nil {
		log.Printf("Google OAuth exchange failed: %v", err)
		http.Error(w, "Google login failed", http.StatusBadGateway)
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		http.Error(w, "Google login failed", http.StatusInternalServerError)
		return
	}
	response, err := config.Client(ctx, token).Do(request)
	if err != nil {
		log.Printf("Google userinfo request failed: %v", err)
		http.Error(w, "Google login failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		log.Printf("Google userinfo returned status %d", response.StatusCode)
		http.Error(w, "Google login failed", http.StatusBadGateway)
		return
	}
	var userInfo struct {
		Email         string `json:"email"`
		VerifiedEmail bool   `json:"verified_email"`
		HostedDomain  string `json:"hd"`
	}
	if err := json.NewDecoder(response.Body).Decode(&userInfo); err != nil || userInfo.Email == "" || !userInfo.VerifiedEmail {
		redirectToLogin(w, r, "google_login_failed")
		return
	}
	accessPolicy, err := policy.FromEnvironment()
	if err != nil {
		log.Printf("Google access policy: %v", err)
		http.Error(w, "Google login is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !accessPolicy.Allows(userInfo.Email, userInfo.HostedDomain) {
		redirectToLogin(w, r, "google_domain")
		return
	}

	applicationLogin, err := exchangeVerifiedEmail(ctx, userInfo.Email)
	if err != nil {
		log.Printf("user-management Google token exchange failed: %v", err)
		var statusErr *exchangeStatusError
		switch {
		case errors.As(err, &statusErr) && statusErr.status == http.StatusForbidden:
			redirectToLogin(w, r, "google_not_registered")
		case errors.As(err, &statusErr) && statusErr.status == http.StatusConflict:
			redirectToLogin(w, r, "google_account_conflict")
		default:
			redirectToLogin(w, r, "google_login_failed")
		}
		return
	}
	if applicationLogin.SignupTicket != "" {
		http.SetCookie(w, &http.Cookie{Name: signupCookie, Value: applicationLogin.SignupTicket, Path: "/", HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: 900})
		http.Redirect(w, r, frontendURL()+"/signup/google", http.StatusTemporaryRedirect)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "jwt", Value: applicationLogin.Token, Path: "/", HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	rabbitmq.PublishLoginEvent(userInfo.Email)
	http.Redirect(w, r, frontendURL()+"/auth/google/callback?google_login=success", http.StatusTemporaryRedirect)
}

func exchangeVerifiedEmail(ctx context.Context, email string) (userManagementLogin, error) {
	host := os.Getenv("UMS_URL")
	if host == "" {
		host = "http://user_management_service:8082"
	}
	payload, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		return userManagementLogin{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/internal/google-login", bytes.NewReader(payload))
	if err != nil {
		return userManagementLogin{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("INTERNAL_AUTH_TOKEN"))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return userManagementLogin{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return userManagementLogin{}, &exchangeStatusError{status: resp.StatusCode}
	}
	var result userManagementLogin
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return userManagementLogin{}, err
	}
	// 202: a roster student without an account; the student-ID step follows.
	if resp.StatusCode == http.StatusAccepted {
		if result.SignupTicket == "" || result.Token != "" {
			return userManagementLogin{}, errors.New("user management returned an invalid signup reply")
		}
		return result, nil
	}
	if result.Token == "" || result.Role == "" || result.UserID == "" || result.SignupTicket != "" {
		return userManagementLogin{}, errors.New("user management returned incomplete login data")
	}
	return result, nil
}

func LogoutHandler(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "jwt", Value: "", Path: "/", HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}
