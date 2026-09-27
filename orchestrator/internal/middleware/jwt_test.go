package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := []byte("0123456789abcdef0123456789abcdef")
	const issuer = "clearsky-identity"
	const audience = "clearsky-api"

	tests := []struct {
		name       string
		method     jwt.SigningMethod
		expiresAt  *jwt.NumericDate
		role       string
		issuer     string
		audience   string
		wantStatus int
	}{
		{name: "valid", method: jwt.SigningMethodHS256, expiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), role: "student", issuer: issuer, audience: audience, wantStatus: http.StatusNoContent},
		{name: "missing expiry", method: jwt.SigningMethodHS256, role: "student", issuer: issuer, audience: audience, wantStatus: http.StatusUnauthorized},
		{name: "wrong algorithm", method: jwt.SigningMethodHS512, expiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), role: "student", issuer: issuer, audience: audience, wantStatus: http.StatusUnauthorized},
		{name: "unknown role", method: jwt.SigningMethodHS256, expiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), role: "administrator", issuer: issuer, audience: audience, wantStatus: http.StatusUnauthorized},
		{name: "wrong issuer", method: jwt.SigningMethodHS256, expiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), role: "student", issuer: "other", audience: audience, wantStatus: http.StatusUnauthorized},
		{name: "wrong audience", method: jwt.SigningMethodHS256, expiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), role: "student", issuer: issuer, audience: "other", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			claims := Claims{
				UserID:        "user-1",
				InstitutionID: "6f0b1a3e-0000-5000-8000-000000000001",
				Role:          tt.role,
				RegisteredClaims: jwt.RegisteredClaims{
					Issuer:    tt.issuer,
					Subject:   "user-1",
					Audience:  jwt.ClaimStrings{tt.audience},
					ExpiresAt: tt.expiresAt,
					IssuedAt:  jwt.NewNumericDate(now),
					ID:        "token-1",
				},
			}
			token, err := jwt.NewWithClaims(tt.method, claims).SignedString(key)
			if err != nil {
				t.Fatalf("sign token: %v", err)
			}

			router := gin.New()
			router.Use(JWTAuthMiddleware(key, issuer, audience))
			router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.Code, tt.wantStatus)
			}
		})
	}
}

func TestJWTAuthMiddlewareAcceptsHTTPOnlyCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now()
	claims := Claims{
		UserID:        "user-1",
		InstitutionID: "6f0b1a3e-0000-5000-8000-000000000001",
		Role:          "student",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "clearsky-identity", Subject: "user-1", Audience: jwt.ClaimStrings{"clearsky-api"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now), ID: "token-1",
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(JWTAuthMiddleware(key, "clearsky-identity", "clearsky-api"))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: token, HttpOnly: true})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusNoContent, resp.Body.String())
	}
}
