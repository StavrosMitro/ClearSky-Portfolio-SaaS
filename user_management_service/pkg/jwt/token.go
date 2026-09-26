package jwt

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	StudentID string `json:"student_id,omitempty"`
	jwt.RegisteredClaims
}

func ValidateConfiguration() error {
	if _, err := signingKey(); err != nil {
		return err
	}
	_, _, err := issuerAudience()
	return err
}

func issuerAudience() (string, string, error) {
	issuer := os.Getenv("JWT_ISSUER")
	audience := os.Getenv("JWT_AUDIENCE")
	if issuer == "" {
		issuer = "clearsky-identity"
	}
	if audience == "" {
		audience = "clearsky-api"
	}
	return issuer, audience, nil
}

func signingKey() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	return []byte(secret), nil
}

// GenerateToken issues a JWT with username instead of email
func GenerateToken(userID, username, role, studentID string) (string, error) {
	jwtKey, err := signingKey()
	if err != nil {
		return "", err
	}
	issuer, audience, err := issuerAudience()
	if err != nil {
		return "", err
	}
	now := time.Now()

	claims := &Claims{
		UserID:    userID,
		Username:  username,
		Role:      role,
		StudentID: studentID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

func ParseToken(tokenStr string) (*Claims, error) {
	jwtKey, err := signingKey()
	if err != nil {
		return nil, err
	}
	issuer, audience, err := issuerAudience()
	if err != nil {
		return nil, err
	}
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return jwtKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithIssuer(issuer), jwt.WithAudience(audience), jwt.WithLeeway(30*time.Second))

	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	if claims.UserID == "" || claims.Subject != claims.UserID || claims.ID == "" || claims.IssuedAt == nil || claims.Role == "" {
		return nil, errors.New("required token claims are missing")
	}

	return claims, nil
}
