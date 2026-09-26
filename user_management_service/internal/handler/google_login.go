package handler

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"

	"user_management_service/internal/accounts"
	"user_management_service/internal/model"
	jwtutil "user_management_service/pkg/jwt"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type internalGoogleLoginRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// InternalGoogleLogin exchanges a Google identity already verified by the
// Google auth service for an application token. User management remains the
// only application-token issuer and the stored user role remains authoritative.
//
// A verified email without an account but present in the student roster
// receives 202 with a short-lived signup ticket instead; the student then
// confirms their student ID before any account is created.
func InternalGoogleLogin(svc *accounts.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := os.Getenv("INTERNAL_AUTH_TOKEN")
		provided, hasBearer := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if len(expected) < 32 || !hasBearer || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req internalGoogleLoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "A valid verified email is required"})
			return
		}
		email, ok := accounts.NormalizeEmail(req.Email)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "A valid verified email is required"})
			return
		}
		var user model.User
		if err := svc.DB.Where("LOWER(username) = ?", email).First(&user).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "User store is unavailable"})
				return
			}
			beginSignup(c, svc, email)
			return
		}
		token, err := jwtutil.GenerateToken(user.ID, user.Username, user.Role, user.StudentID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create session"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "role": user.Role, "user_id": user.ID})
	}
}

func beginSignup(c *gin.Context, svc *accounts.Service, email string) {
	ticket, err := svc.BeginGoogleSignup(email)
	if err == nil {
		c.JSON(http.StatusAccepted, gin.H{"signup_ticket": ticket})
		return
	}
	var accountErr *accounts.Error
	switch {
	case errors.As(err, &accountErr) && accountErr.Code == "CONFLICT":
		c.JSON(http.StatusConflict, gin.H{"error": accountErr.Message})
	case errors.As(err, &accountErr) && accountErr.Code == "FORBIDDEN":
		c.JSON(http.StatusForbidden, gin.H{"error": "Google account is not provisioned"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "User store is unavailable"})
	}
}
