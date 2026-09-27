package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

// TokenInfoURL is Google's ID-token verification endpoint (replaceable in tests).
var TokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"

// VerifyIDToken checks a Google ID token (audience, issuer, verified email)
// and returns the Google identity. Used by API clients that obtain an ID
// token themselves instead of the browser redirect flow.
func VerifyIDToken(ctx context.Context, idToken string) (Identity, error) {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	if clientID == "" {
		return Identity{}, errors.New("GOOGLE_CLIENT_ID is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TokenInfoURL+"?id_token="+url.QueryEscape(idToken), nil)
	if err != nil {
		return Identity{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Identity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("tokeninfo status %d", resp.StatusCode)
	}
	var info struct {
		Audience      string `json:"aud"`
		Issuer        string `json:"iss"`
		Email         string `json:"email"`
		EmailVerified string `json:"email_verified"`
		HostedDomain  string `json:"hd"`
		Subject       string `json:"sub"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Identity{}, err
	}
	validIssuer := info.Issuer == "accounts.google.com" || info.Issuer == "https://accounts.google.com"
	if info.Audience != clientID || !validIssuer || info.Email == "" || info.EmailVerified != "true" {
		return Identity{}, errors.New("Google token claims are invalid")
	}
	return Identity{Email: info.Email, VerifiedEmail: true, HostedDomain: info.HostedDomain, Subject: info.Subject}, nil
}
