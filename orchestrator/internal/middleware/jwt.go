package middleware

import (
	"net/http"
	"strings"
	"time"

	"orchestrator/internal/api"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username,omitempty"`
	Role      string `json:"role"`
	StudentID string `json:"student_id,omitempty"` // Add student_id field
	jwt.RegisteredClaims
}

func JWTAuthMiddleware(jwtKey []byte, issuer, audience string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		var tokenStr string
		if authHeader != "" {
			parts := strings.Fields(authHeader)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				api.Abort(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Invalid Authorization header")
				return
			}
			tokenStr = parts[1]
		} else if cookie, err := c.Cookie("jwt"); err == nil {
			tokenStr = cookie
		}
		if tokenStr == "" {
			api.Abort(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Authentication is required")
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		},
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithIssuer(issuer),
			jwt.WithAudience(audience),
			jwt.WithLeeway(30*time.Second),
		)
		if err != nil || !token.Valid {
			api.Abort(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Invalid or expired token")
			return
		}
		if claims.Subject == "" || claims.UserID != claims.Subject || claims.ID == "" || claims.IssuedAt == nil || !validRole(claims.Role) {
			api.Abort(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Invalid token claims")
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username) // Add username to context
		c.Set("role", claims.Role)
		c.Set("student_id", claims.StudentID) // Set student_id in context
		// Forwarded to user management, which re-verifies it for account administration.
		c.Set(rawTokenKey, tokenStr)

		c.Next()
	}
}

func validRole(role string) bool {
	switch role {
	case "student", "instructor", "institution_representative":
		return true
	default:
		return false
	}
}

const rawTokenKey = "raw_jwt"

// Helper functions for other services to use
func GetRawToken(c *gin.Context) string {
	return c.GetString(rawTokenKey)
}

func GetUserID(c *gin.Context) string {
	return c.GetString("user_id")
}

func GetUsername(c *gin.Context) string {
	return c.GetString("username")
}

func GetRole(c *gin.Context) string {
	return c.GetString("role")
}

func GetStudentID(c *gin.Context) string {
	return c.GetString("student_id")
}

func IsStudent(c *gin.Context) bool {
	return GetRole(c) == "student"
}

func RequireStudentID() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsStudent(c) && GetStudentID(c) == "" {
			api.Abort(c, http.StatusBadRequest, api.CodeInvalidRequest, "Student ID is required for this operation")
			return
		}
		c.Next()
	}
}
