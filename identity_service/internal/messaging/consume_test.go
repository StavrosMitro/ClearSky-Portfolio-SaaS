package messaging

import (
	"context"
	"encoding/json"
	"testing"

	"identity_service/internal/accounts"
	"identity_service/internal/model"
	"identity_service/internal/store"
	jwtutil "identity_service/pkg/jwt"

	"clearsky/contracts/ids"
	"clearsky/contracts/pgtest"
	"clearsky/contracts/rpc"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm/logger"
)

var institution = ids.Institution("NTUA")

func testService(t *testing.T) *accounts.Service {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-signing-key-with-at-least-32-bytes")
	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_AUDIENCE", "")
	db, err := store.Open(pgtest.URL(t, store.Migrations()))
	if err != nil {
		t.Fatal(err)
	}
	db.Logger = logger.Discard
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &accounts.Service{DB: db, AppURL: "http://localhost:3000"}
}

func createUser(t *testing.T, svc *accounts.Service, username, role, password string) model.User {
	t.Helper()
	user := model.User{ID: uuid.NewString(), InstitutionID: institution, Username: username, Role: role}
	if role == "student" {
		user.StudentID = "031" + uuid.NewString()[:5]
	}
	if password != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		user.PasswordHash = string(hash)
	}
	if err := svc.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func call(t *testing.T, svc *accounts.Service, msgType string, req AuthRequest) (any, error) {
	t.Helper()
	body, _ := json.Marshal(req)
	return Handler(svc)(context.Background(), msgType, body)
}

func code(err error) string {
	if rpcErr, ok := rpc.AsError(err); ok {
		return rpcErr.Code
	}
	return ""
}

func TestLegacySelfRegistrationIsRejected(t *testing.T) {
	svc := testService(t)
	_, err := call(t, svc, "register", AuthRequest{Username: "mallory", Password: "password123", StudentID: "03100001"})
	if code(err) != rpc.CodeInvalidRequest {
		t.Fatalf("register = %v", err)
	}
}

func TestLoginIssuesTokenWithInstitution(t *testing.T) {
	svc := testService(t)
	createUser(t, svc, "alice@uni.example", "student", "a-good-password")
	result, err := call(t, svc, "login", AuthRequest{Username: " Alice@Uni.Example ", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	auth := result.(AuthResult)
	claims, err := jwtutil.ParseToken(auth.Token)
	if err != nil || claims.InstitutionID != institution || claims.Role != "student" || claims.StudentID == "" {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	if _, err := call(t, svc, "login", AuthRequest{Username: "alice@uni.example", Password: "wrong"}); code(err) != rpc.CodeInvalidCredentials {
		t.Fatalf("wrong password = %v", err)
	}
}

func TestAdministrationRequiresRepresentativeToken(t *testing.T) {
	svc := testService(t)
	rep := createUser(t, svc, "registrar@uni.example", "institution_representative", "")
	instructor := createUser(t, svc, "prof@uni.example", "instructor", "")
	token := func(u model.User) string {
		raw, err := jwtutil.GenerateToken(u.ID, u.InstitutionID, u.Username, u.Role, u.StudentID)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	roster := "student_id,email\n03100001,alice@uni.example\n"
	for name, actor := range map[string]string{"missing": "", "forged": "x", "instructor": token(instructor)} {
		if _, err := call(t, svc, "import_student_roster", AuthRequest{CSV: roster, ActorToken: actor}); code(err) != rpc.CodeUnauthenticated && code(err) != rpc.CodeForbidden {
			t.Fatalf("%s actor: %v", name, err)
		}
	}
	if _, err := call(t, svc, "import_student_roster", AuthRequest{CSV: roster, ActorToken: token(rep)}); err != nil {
		t.Fatal(err)
	}
	var entry model.StudentRosterEntry
	if err := svc.DB.First(&entry).Error; err != nil || entry.UploadedBy != rep.ID || entry.InstitutionID != institution {
		t.Fatalf("roster entry = %+v, %v", entry, err)
	}
}

func TestChangePasswordChecksTheCurrentPassword(t *testing.T) {
	svc := testService(t)
	createUser(t, svc, "alice@uni.example", "student", "old-password")
	if _, err := call(t, svc, "change_password", AuthRequest{Username: "alice@uni.example", OldPassword: "wrong", NewPassword: "new-password"}); code(err) != rpc.CodeInvalidCredentials {
		t.Fatalf("wrong current password = %v", err)
	}
	if _, err := call(t, svc, "change_password", AuthRequest{Username: "alice@uni.example", OldPassword: "old-password", NewPassword: "new-password"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, svc, "login", AuthRequest{Username: "alice@uni.example", Password: "new-password"}); err != nil {
		t.Fatalf("new password: %v", err)
	}
}
