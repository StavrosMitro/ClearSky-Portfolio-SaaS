// Package api is a small client for the public ClearSky API (through the
// proxy) and the Mailpit inbox. The seed tool and the end-to-end tests use it
// to act as real users: every account is created and activated through the
// same emails and endpoints a person would use.
package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"time"
)

// Error is a failed API call.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, e.Message) }

// Is reports whether err is an API error with this HTTP status.
func Is(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// Config is shared by every session.
type Config struct {
	BaseURL  string // e.g. https://localhost (the API is BaseURL + /api)
	Insecure bool   // accept Caddy's local CA
}

// Session is one browser: it keeps the HttpOnly session cookie.
type Session struct {
	cfg    Config
	http   *http.Client
	Name   string // for messages only
	MaxTry int    // attempts while rate-limited (429)
}

// NewSession returns a signed-out session.
func NewSession(cfg Config, name string) *Session {
	jar, _ := cookiejar.New(nil)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.Insecure} //nolint:gosec // local CA, opt-in
	return &Session{cfg: cfg, Name: name, MaxTry: 30, http: &http.Client{
		Jar: jar, Transport: transport, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// JSON calls method path (relative to /api) with an optional JSON body and
// decodes the response data into out (when not nil).
func (s *Session) JSON(ctx context.Context, method, path string, body, out any) error {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return err
		}
	}
	return s.do(ctx, method, path, "application/json", func() io.Reader { return bytes.NewReader(raw) }, out)
}

// Upload posts a multipart form with one file field.
func (s *Session) Upload(ctx context.Context, path string, fields map[string]string, fileField, filename string, data []byte, out any) error {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := form.WriteField(k, v); err != nil {
			return err
		}
	}
	part, err := form.CreateFormFile(fileField, filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	raw := buf.Bytes()
	return s.do(ctx, http.MethodPost, path, form.FormDataContentType(), func() io.Reader { return bytes.NewReader(raw) }, out)
}

func (s *Session) do(ctx context.Context, method, path, contentType string, body func() io.Reader, out any) error {
	url := strings.TrimRight(s.cfg.BaseURL, "/") + "/api" + path
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, body())
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := s.http.Do(req)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < s.MaxTry {
			wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			select {
			case <-time.After(time.Duration(max(wait, 1)) * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return &Error{Status: resp.StatusCode, Code: "NOT_JSON", Message: truncate(string(raw))}
		}
		if resp.StatusCode >= 300 || env.Error != nil {
			apiErr := &Error{Status: resp.StatusCode}
			if env.Error != nil {
				apiErr.Code, apiErr.Message = env.Error.Code, env.Error.Message
			}
			return apiErr
		}
		if out != nil && len(env.Data) > 0 {
			return json.Unmarshal(env.Data, out)
		}
		return nil
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// Me is GET /user/me.
type Me struct {
	UserID        string `json:"user_id"`
	InstitutionID string `json:"institution_id"`
	Username      string `json:"username"`
	Role          string `json:"role"`
	StudentID     string `json:"student_id"`
}

// Login signs the session in.
func (s *Session) Login(ctx context.Context, username, password string) error {
	return s.JSON(ctx, http.MethodPost, "/user/login", map[string]string{"username": username, "password": password}, nil)
}

// Me returns the signed-in user.
func (s *Session) Me(ctx context.Context) (Me, error) {
	var me Me
	err := s.JSON(ctx, http.MethodGet, "/user/me", nil, &me)
	return me, err
}
