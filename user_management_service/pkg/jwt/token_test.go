package jwt

import "testing"

func TestGenerateAndParseToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_ISSUER", "clearsky-identity")
	t.Setenv("JWT_AUDIENCE", "clearsky-api")

	token, err := GenerateToken("user-1", "student@example.com", "student", "student-1")
	if err != nil {
		t.Fatalf("GenerateToken() returned an error: %v", err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken() returned an error: %v", err)
	}
	if claims.UserID != "user-1" || claims.Subject != "user-1" || claims.Role != "student" || claims.Issuer != "clearsky-identity" || len(claims.Audience) != 1 || claims.Audience[0] != "clearsky-api" || claims.IssuedAt == nil || claims.ID == "" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestParseTokenRejectsWrongAudience(t *testing.T) {
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_ISSUER", "clearsky-identity")
	t.Setenv("JWT_AUDIENCE", "first-audience")
	token, err := GenerateToken("user-1", "student@example.com", "student", "student-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_AUDIENCE", "different-audience")
	if _, err := ParseToken(token); err == nil {
		t.Fatal("ParseToken accepted a token for another audience")
	}
}

func TestGenerateTokenRejectsShortSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "too-short")
	t.Setenv("JWT_ISSUER", "clearsky-identity")
	t.Setenv("JWT_AUDIENCE", "clearsky-api")
	if _, err := GenerateToken("user-1", "student@example.com", "student", "student-1"); err == nil {
		t.Fatal("GenerateToken() accepted an unsafe secret")
	}
}
