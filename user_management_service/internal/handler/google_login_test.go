package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"user_management_service/internal/accounts"
	"user_management_service/internal/model"
	jwtutil "user_management_service/pkg/jwt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testInternalToken = "internal-token-with-at-least-32-bytes"

func googleLoginTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "test-signing-key-with-at-least-32-bytes")
	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_AUDIENCE", "")
	t.Setenv("INTERNAL_AUTH_TOKEN", testInternalToken)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "users.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.StudentRosterEntry{}, &model.AccountToken{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func postGoogleLogin(db *gorm.DB, authorization, body string) *httptest.ResponseRecorder {
	router := gin.New()
	router.POST("/internal/google-login", InternalGoogleLogin(&accounts.Service{DB: db}))
	req := httptest.NewRequest(http.MethodPost, "/internal/google-login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestInternalGoogleLoginRejectsMissingOrWrongSecret(t *testing.T) {
	db := googleLoginTestDB(t)
	user := model.User{ID: uuid.NewString(), Username: "teacher@example.com", Role: "instructor"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	body := `{"email":"teacher@example.com"}`
	wrongSameLength := strings.Repeat("x", len(testInternalToken))

	for name, authorization := range map[string]string{
		"missing header":        "",
		"wrong secret":          "Bearer " + wrongSameLength,
		"secret without bearer": testInternalToken,
		"wrong scheme":          "Basic " + testInternalToken,
		"prefix of secret":      "Bearer " + testInternalToken[:len(testInternalToken)-1],
	} {
		t.Run(name, func(t *testing.T) {
			rec := postGoogleLogin(db, authorization, body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "token") {
				t.Fatalf("unauthorized response leaked a token: %s", rec.Body.String())
			}
		})
	}
}

func TestInternalGoogleLoginRejectsWhenServiceSecretIsUnconfigured(t *testing.T) {
	db := googleLoginTestDB(t)
	for name, configured := range map[string]string{"unset": "", "too short": "short-secret"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("INTERNAL_AUTH_TOKEN", configured)
			rec := postGoogleLogin(db, "Bearer "+configured, `{"email":"teacher@example.com"}`)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestInternalGoogleLoginPreservesStoredRole(t *testing.T) {
	db := googleLoginTestDB(t)
	stored := model.User{ID: uuid.NewString(), Username: "student@example.com", Role: "student", StudentID: "03100000"}
	if err := db.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}

	// Extra fields must not influence the issued identity.
	body := `{"email":"student@example.com","role":"institution_representative","student_id":"99999999","user_id":"attacker"}`
	rec := postGoogleLogin(db, "Bearer "+testInternalToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Token  string `json:"token"`
		Role   string `json:"role"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Role != "student" || response.UserID != stored.ID {
		t.Fatalf("response identity = %+v, want stored user", response)
	}
	claims, err := jwtutil.ParseToken(response.Token)
	if err != nil {
		t.Fatalf("issued token is invalid: %v", err)
	}
	if claims.Role != "student" || claims.UserID != stored.ID || claims.Subject != stored.ID || claims.StudentID != "03100000" || claims.Username != "student@example.com" {
		t.Fatalf("token claims do not match the stored user: %+v", claims)
	}

	var after model.User
	if err := db.First(&after, "id = ?", stored.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Role != "student" || after.StudentID != "03100000" {
		t.Fatalf("stored user was modified: %+v", after)
	}
}

func TestInternalGoogleLoginRejectsUnprovisionedAndInvalidEmail(t *testing.T) {
	db := googleLoginTestDB(t)

	rec := postGoogleLogin(db, "Bearer "+testInternalToken, `{"email":"unknown@example.com"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unprovisioned status = %d, want 403", rec.Code)
	}
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unprovisioned login created an account (count=%d, err=%v)", count, err)
	}

	rec = postGoogleLogin(db, "Bearer "+testInternalToken, `{"email":"not-an-email"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid email status = %d, want 400", rec.Code)
	}
}

func TestInternalGoogleLoginStartsSignupOnlyForRosterStudents(t *testing.T) {
	db := googleLoginTestDB(t)
	if err := db.Create(&model.StudentRosterEntry{StudentID: "03100001", Email: "new.student@uni.example"}).Error; err != nil {
		t.Fatal(err)
	}

	rec := postGoogleLogin(db, "Bearer "+testInternalToken, `{"email":"New.Student@uni.example"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("roster student status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Token        string `json:"token"`
		SignupTicket string `json:"signup_ticket"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SignupTicket == "" || response.Token != "" {
		t.Fatalf("expected only a signup ticket, got %+v", response)
	}
	var users int64
	if err := db.Model(&model.User{}).Count(&users).Error; err != nil || users != 0 {
		t.Fatalf("starting a signup must not create an account (count=%d, err=%v)", users, err)
	}

	svc := &accounts.Service{DB: db}
	if _, err := svc.CompleteGoogleSignup(response.SignupTicket, "03100001"); err != nil {
		t.Fatalf("complete signup with the issued ticket: %v", err)
	}
	rec = postGoogleLogin(db, "Bearer "+testInternalToken, `{"email":"new.student@uni.example"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("registered student status = %d, want 200", rec.Code)
	}
}
