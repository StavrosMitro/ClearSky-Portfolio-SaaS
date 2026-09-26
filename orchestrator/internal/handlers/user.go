package handlers

import (
	"log"
	"net/http"
	"orchestrator/internal/middleware"
	"os"
	"strings"

	"orchestrator/internal/api"

	"github.com/gin-gonic/gin"
	amqp "github.com/rabbitmq/amqp091-go"
)

type authResult struct {
	Token  string `json:"token"`
	Role   string `json:"role"`
	UserID string `json:"user_id"`
}

func setSessionCookie(c *gin.Context, token string, maxAge int) {
	secure := strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("jwt", token, maxAge, "/", "", secure, true)
}

func HandleUserLogin(c *gin.Context, m Messenger) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Username and password are required", nil)
		return
	}
	var response authResult
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]interface{}{"type": "login", "username": req.Username, "password": req.Password}, &response); err != nil {
		messagingError(c, err)
		return
	}
	if response.Token == "" || response.Role == "" || response.UserID == "" {
		api.Failure(c, http.StatusBadGateway, api.CodeInvalidServiceReply, "Authentication service returned an invalid response", nil)
		return
	}
	setSessionCookie(c, response.Token, 24*60*60)
	api.Success(c, http.StatusOK, gin.H{"role": response.Role, "user_id": response.UserID})
}
func HandleUserGoogleLogin(c *gin.Context, m Messenger) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "A Google ID token is required", nil)
		return
	}
	var identity struct {
		Email string `json:"email"`
	}
	if err := callJSON(c.Request.Context(), m, "auth.login.google", map[string]interface{}{"token": req.Token}, &identity); err != nil {
		messagingError(c, err)
		return
	}
	if identity.Email == "" {
		api.Failure(c, http.StatusBadGateway, api.CodeInvalidServiceReply, "Google authentication returned an invalid identity", nil)
		return
	}
	var response authResult
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]interface{}{"type": "google_login", "username": identity.Email}, &response); err != nil {
		messagingError(c, err)
		return
	}
	if response.Token == "" || response.Role == "" || response.UserID == "" {
		api.Failure(c, http.StatusBadGateway, api.CodeInvalidServiceReply, "Authentication service returned an invalid response", nil)
		return
	}
	setSessionCookie(c, response.Token, 24*60*60)
	api.Success(c, http.StatusOK, gin.H{"role": response.Role, "user_id": response.UserID})
}
func HandleUserChangePassword(c *gin.Context, m Messenger) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Old password and a new password of at least 8 characters are required", nil)
		return
	}
	username := middleware.GetUsername(c)
	if username == "" {
		api.Failure(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Authenticated username is required", nil)
		return
	}
	var response map[string]interface{}
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]interface{}{"type": "change_password", "username": username, "old_password": req.OldPassword, "new_password": req.NewPassword}, &response); err != nil {
		messagingError(c, err)
		return
	}
	api.Success(c, http.StatusOK, response)
}

func HandleUserLogout(c *gin.Context) {
	setSessionCookie(c, "", -1)
	api.Success(c, http.StatusOK, gin.H{"message": "Logged out"})
}
func HandleUserCreated(d amqp.Delivery) {
	log.Printf("[Orchestrator] user.created event received routing_key=%s", d.RoutingKey)
	if err := d.Ack(false); err != nil {
		log.Printf("[Orchestrator] failed to acknowledge user.created event: %v", err)
	}
}
