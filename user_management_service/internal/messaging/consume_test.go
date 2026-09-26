package messaging

import (
	"path/filepath"
	"testing"

	"user_management_service/internal/accounts"
	"user_management_service/internal/model"
	jwtutil "user_management_service/pkg/jwt"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testService(t *testing.T) *accounts.Service {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-signing-key-with-at-least-32-bytes")
	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_AUDIENCE", "")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "users.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.StudentRosterEntry{}, &model.AccountToken{}); err != nil {
		t.Fatal(err)
	}
	return &accounts.Service{DB: db, AppURL: "http://localhost:3000"}
}

func createUser(t *testing.T, db *gorm.DB, username, role, password string) model.User {
	t.Helper()
	user := model.User{ID: uuid.NewString(), Username: username, Role: role}
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		user.PasswordHash = string(hash)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func tokenFor(t *testing.T, user model.User) string {
	t.Helper()
	token, err := jwtutil.GenerateToken(user.ID, user.Username, user.Role, user.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func errorCode(response rpcEnvelope) string {
	if response.Error == nil {
		return ""
	}
	return response.Error.Code
}

func TestLegacySelfRegistrationIsRejected(t *testing.T) {
	svc := testService(t)
	response := handleAuthRequest(svc, AuthRequest{Type: "register", Username: "mallory", Password: "password123", StudentID: "03100001"})
	if errorCode(response) != "INVALID_REQUEST" {
		t.Fatalf("register response = %+v, want INVALID_REQUEST", response)
	}
	var count int64
	if err := svc.DB.Model(&model.User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("legacy register created an account (count=%d, err=%v)", count, err)
	}
}

func TestAccountAdministrationRequiresRepresentativeToken(t *testing.T) {
	svc := testService(t)
	rep := createUser(t, svc.DB, "registrar@uni.example", "institution_representative", "")
	instructor := createUser(t, svc.DB, "prof@uni.example", "instructor", "")
	roster := "student_id,email\n03100001,alice@uni.example\n"

	for name, actor := range map[string]string{"missing": "", "forged": "not-a-jwt", "instructor": tokenFor(t, instructor)} {
		t.Run(name, func(t *testing.T) {
			for _, req := range []AuthRequest{
				{Type: "import_student_roster", CSV: roster, ActorToken: actor},
				{Type: "create_instructor", Email: "new.prof@uni.example", ActorToken: actor},
			} {
				if code := errorCode(handleAuthRequest(svc, req)); code != "UNAUTHENTICATED" && code != "FORBIDDEN" {
					t.Fatalf("%s with %s actor = %q", req.Type, name, code)
				}
			}
		})
	}
	var entries int64
	if err := svc.DB.Model(&model.StudentRosterEntry{}).Count(&entries).Error; err != nil || entries != 0 {
		t.Fatalf("unauthorized import changed the roster (entries=%d, err=%v)", entries, err)
	}

	response := handleAuthRequest(svc, AuthRequest{Type: "import_student_roster", CSV: roster, ActorToken: tokenFor(t, rep)})
	if response.Error != nil {
		t.Fatalf("representative import failed: %+v", response.Error)
	}
	var entry model.StudentRosterEntry
	if err := svc.DB.First(&entry).Error; err != nil || entry.UploadedBy != rep.ID {
		t.Fatalf("import must record the verified uploader: %+v, %v", entry, err)
	}
}

func TestLoginRejectsAccountsWithoutPassword(t *testing.T) {
	svc := testService(t)
	createUser(t, svc.DB, "prof@uni.example", "instructor", "")
	response := handleAuthRequest(svc, AuthRequest{Type: "login", Username: "prof@uni.example", Password: "anything"})
	if errorCode(response) != "INVALID_CREDENTIALS" {
		t.Fatalf("login without password = %+v", response)
	}
}

func TestLoginAcceptsEmailInAnyCase(t *testing.T) {
	svc := testService(t)
	createUser(t, svc.DB, "alice@uni.example", "student", "a-good-password")
	response := handleAuthRequest(svc, AuthRequest{Type: "login", Username: " Alice@Uni.Example ", Password: "a-good-password"})
	if response.Error != nil {
		t.Fatalf("login failed: %+v", response.Error)
	}
	if result, ok := response.Data.(AuthResult); !ok || result.Token == "" || result.Role != "student" {
		t.Fatalf("login data = %#v", response.Data)
	}
}
