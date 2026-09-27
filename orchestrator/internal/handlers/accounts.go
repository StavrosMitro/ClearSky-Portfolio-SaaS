package handlers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/internal/api"
	"orchestrator/internal/middleware"

	"github.com/gin-gonic/gin"
)

const (
	googleSignupCookie = "google_signup"
	maxRosterBytes     = 2 << 20
)

// HandleStudentRegistration starts roster-verified student registration: user
// management emails a confirmation link when the student ID and university
// email match the secretariat's roster.
func HandleStudentRegistration(c *gin.Context, m Messenger) {
	var req struct {
		StudentID string `json:"student_id" binding:"required"`
		Email     string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Student ID and university email are required", nil)
		return
	}
	var response map[string]any
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]any{"type": "request_student_activation", "student_id": req.StudentID, "email": req.Email}, &response); err != nil {
		accountError(c, err)
		return
	}
	api.Success(c, http.StatusAccepted, response)
}

// HandleAccountActivation consumes an emailed link and sets the password.
func HandleAccountActivation(c *gin.Context, m Messenger) {
	var req struct {
		Token    string `json:"token" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The link token and a password are required", nil)
		return
	}
	var response struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]any{"type": "complete_activation", "token": req.Token, "password": req.Password}, &response); err != nil {
		accountError(c, err)
		return
	}
	api.Success(c, http.StatusOK, response)
}

// HandleGoogleSignup finishes a first Google login: the HttpOnly signup ticket
// set by Google auth plus the student ID the student typed.
func HandleGoogleSignup(c *gin.Context, m Messenger) {
	ticket, err := c.Cookie(googleSignupCookie)
	if err != nil || ticket == "" {
		api.Failure(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Your Google sign-up session has expired; sign in with Google again", nil)
		return
	}
	var req struct {
		StudentID string `json:"student_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Your student ID is required", nil)
		return
	}
	var response authResult
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]any{"type": "complete_google_signup", "signup_ticket": ticket, "student_id": req.StudentID}, &response); err != nil {
		accountError(c, err)
		return
	}
	if response.Token == "" || response.Role == "" || response.UserID == "" {
		api.Failure(c, http.StatusBadGateway, api.CodeInvalidServiceReply, "Authentication service returned an invalid response", nil)
		return
	}
	setSessionCookie(c, response.Token, 24*60*60)
	c.SetCookie(googleSignupCookie, "", -1, "/", "", strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true"), true)
	api.Success(c, http.StatusOK, gin.H{"role": response.Role, "user_id": response.UserID})
}

// HandleForgotPassword asks for a password-reset email. The reply is the same
// whether or not an account exists, so it cannot reveal registered emails.
func HandleForgotPassword(c *gin.Context, m Messenger) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Your email is required", nil)
		return
	}
	var response map[string]any
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]any{"type": "request_password_reset", "email": req.Email}, &response); err != nil {
		accountError(c, err)
		return
	}
	api.Success(c, http.StatusAccepted, response)
}

// HandleCreateInstructor lets the secretariat register an instructor, who
// receives an email link to choose a password.
func HandleCreateInstructor(c *gin.Context, m Messenger) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The instructor's email is required", nil)
		return
	}
	var response struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Resent bool   `json:"resent"`
	}
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]any{"type": "create_instructor", "email": req.Email, "actor_token": middleware.GetRawToken(c)}, &response); err != nil {
		accountError(c, err)
		return
	}
	status := http.StatusCreated
	if response.Resent {
		status = http.StatusOK
	}
	api.Success(c, status, response)
}

// HandleStudentRosterUpload imports the secretariat's CSV of student IDs and
// university emails.
func HandleStudentRosterUpload(c *gin.Context, m Messenger) {
	file, err := c.FormFile("file")
	if err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "A CSV file is required", nil)
		return
	}
	if !strings.EqualFold(filepath.Ext(file.Filename), ".csv") {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Only .csv files are allowed; export the spreadsheet as CSV", nil)
		return
	}
	if file.Size > maxRosterBytes {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The roster file must be smaller than 2 MiB", nil)
		return
	}
	src, err := file.Open()
	if err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The uploaded file could not be read", nil)
		return
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, maxRosterBytes+1))
	if err != nil {
		api.Internal(c)
		return
	}
	if len(data) > maxRosterBytes {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The roster file must be smaller than 2 MiB", nil)
		return
	}
	var response map[string]any
	if err := callJSON(c.Request.Context(), m, "auth.request", map[string]any{"type": "import_student_roster", "csv": string(data), "actor_token": middleware.GetRawToken(c)}, &response); err != nil {
		accountError(c, err)
		return
	}
	api.Success(c, http.StatusOK, response)
}

// accountError is serviceError: identity's messages are written for users.
func accountError(c *gin.Context, err error) { serviceError(c, err) }
