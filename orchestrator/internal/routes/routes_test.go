package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mw "orchestrator/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "clearsky-identity"
	testAudience = "clearsky-api"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

type recordingMessenger struct{ bodies [][]byte }

func (r *recordingMessenger) Call(_ context.Context, _ string, body []byte) ([]byte, error) {
	r.bodies = append(r.bodies, body)
	return []byte(`{"version":1,"data":{"user_id":"u1","email":"prof@uni.example","resent":false}}`), nil
}
func (r *recordingMessenger) Send(context.Context, string, []byte) error { return nil }
func (r *recordingMessenger) Ready() bool                                { return true }

func signedToken(t *testing.T, role string) string {
	t.Helper()
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, mw.Claims{
		UserID: "user-1",
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: testIssuer, Subject: "user-1", Audience: jwt.ClaimStrings{testAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now), ID: "jti-1",
		},
	}).SignedString(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestAccountAdministrationIsLimitedToRepresentatives(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/institution/instructors", "/institution/student-roster"} {
		for role, want := range map[string]int{"": http.StatusUnauthorized, "student": http.StatusForbidden, "instructor": http.StatusForbidden} {
			m := &recordingMessenger{}
			router := SetupRouter(m, []string{"http://localhost:3000"}, testKey, testIssuer, testAudience)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"email":"prof@uni.example"}`))
			req.Header.Set("Content-Type", "application/json")
			if role != "" {
				req.AddCookie(&http.Cookie{Name: "jwt", Value: signedToken(t, role)})
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != want || len(m.bodies) != 0 {
				t.Fatalf("%s as %q: status %d (want %d), calls %d", path, role, rec.Code, want, len(m.bodies))
			}
		}
	}
}

func TestRepresentativeTokenIsForwardedForReverification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &recordingMessenger{}
	router := SetupRouter(m, []string{"http://localhost:3000"}, testKey, testIssuer, testAudience)
	token := signedToken(t, "institution_representative")
	req := httptest.NewRequest(http.MethodPost, "/institution/instructors", strings.NewReader(`{"email":"prof@uni.example"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "jwt", Value: token})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated || len(m.bodies) != 1 {
		t.Fatalf("status %d, calls %d: %s", rec.Code, len(m.bodies), rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(m.bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	if body["actor_token"] != token {
		t.Fatal("the caller's JWT must be forwarded so user management can re-verify it")
	}
}
