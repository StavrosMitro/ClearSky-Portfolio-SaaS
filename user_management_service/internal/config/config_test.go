package config

import (
	"path/filepath"
	"testing"

	"user_management_service/internal/model"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const validBootstrapPassword = "correct-horse-battery"

func setBootstrapEnvironment(t *testing.T, username, password string) {
	t.Helper()
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", username)
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", password)
}

func setupTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("DATABASE_DSN", filepath.Join(t.TempDir(), "users.db"))
	db, err := SetupDatabase()
	if err != nil {
		t.Fatalf("SetupDatabase: %v", err)
	}
	closeOnCleanup(t, db)
	return db
}

func closeOnCleanup(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
}

func allUsers(t *testing.T, db *gorm.DB) []model.User {
	t.Helper()
	var users []model.User
	if err := db.Find(&users).Error; err != nil {
		t.Fatalf("list users: %v", err)
	}
	return users
}

func TestBootstrapAdminFromEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		want     bool
		wantErr  bool
	}{
		{name: "unset", want: false},
		{name: "username only", username: "root@example.com", wantErr: true},
		{name: "password only", password: validBootstrapPassword, wantErr: true},
		{name: "short password", username: "root@example.com", password: "elevenchars", wantErr: true},
		{name: "whitespace username", username: "   ", password: validBootstrapPassword, wantErr: true},
		{name: "valid", username: "root@example.com", password: "twelve-chars", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBootstrapEnvironment(t, tt.username, tt.password)
			admin, err := BootstrapAdminFromEnvironment()
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if (admin != nil) != tt.want {
				t.Fatalf("admin = %+v, want configured=%v", admin, tt.want)
			}
		})
	}
}

func TestSetupDatabaseCreatesNoDefaultAccount(t *testing.T) {
	setBootstrapEnvironment(t, "", "")
	db := setupTestDatabase(t)

	if users := allUsers(t, db); len(users) != 0 {
		t.Fatalf("expected no accounts without explicit bootstrap, got %d", len(users))
	}
}

func TestSetupDatabaseCreatesExplicitBootstrapAdmin(t *testing.T) {
	setBootstrapEnvironment(t, "root@example.com", validBootstrapPassword)
	db := setupTestDatabase(t)

	users := allUsers(t, db)
	if len(users) != 1 {
		t.Fatalf("expected exactly the bootstrap administrator, got %d accounts", len(users))
	}
	admin := users[0]
	if admin.Username != "root@example.com" || admin.Role != "institution_representative" || admin.StudentID != "" {
		t.Fatalf("unexpected bootstrap administrator: %+v", admin)
	}
	if _, err := uuid.Parse(admin.ID); err != nil {
		t.Fatalf("bootstrap administrator ID %q is not a UUID: %v", admin.ID, err)
	}
	if admin.PasswordHash == validBootstrapPassword || bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(validBootstrapPassword)) != nil {
		t.Fatal("bootstrap password was not stored as a matching bcrypt hash")
	}
}

func TestSetupDatabaseRejectsInvalidBootstrapConfiguration(t *testing.T) {
	for name, env := range map[string][2]string{
		"short password": {"root@example.com", "short"},
		"missing half":   {"root@example.com", ""},
	} {
		t.Run(name, func(t *testing.T) {
			setBootstrapEnvironment(t, env[0], env[1])
			t.Setenv("DATABASE_DSN", filepath.Join(t.TempDir(), "users.db"))
			if db, err := SetupDatabase(); err == nil {
				closeOnCleanup(t, db)
				t.Fatal("expected invalid bootstrap configuration to fail startup")
			}
		})
	}
}

func TestEnsureBootstrapAdminLeavesExistingUserUnchanged(t *testing.T) {
	setBootstrapEnvironment(t, "", "")
	db := setupTestDatabase(t)
	original := model.User{ID: uuid.NewString(), Username: "root@example.com", PasswordHash: "existing-hash", Role: "student", StudentID: "03100000"}
	if err := db.Create(&original).Error; err != nil {
		t.Fatalf("create existing user: %v", err)
	}

	created, err := EnsureBootstrapAdmin(db, BootstrapAdmin{Username: "root@example.com", Password: validBootstrapPassword})
	if err != nil || created {
		t.Fatalf("EnsureBootstrapAdmin = (%v, %v), want (false, nil)", created, err)
	}
	users := allUsers(t, db)
	if len(users) != 1 {
		t.Fatalf("expected 1 account, got %d", len(users))
	}
	got := users[0]
	if got.ID != original.ID || got.PasswordHash != original.PasswordHash || got.Role != original.Role || got.StudentID != original.StudentID {
		t.Fatalf("existing user was modified: %+v", got)
	}
}

func TestEnsureBootstrapAdminRejectsWeakPassword(t *testing.T) {
	setBootstrapEnvironment(t, "", "")
	db := setupTestDatabase(t)

	if _, err := EnsureBootstrapAdmin(db, BootstrapAdmin{Username: "root@example.com", Password: "admin"}); err == nil {
		t.Fatal("expected a short bootstrap password to be rejected")
	}
	if users := allUsers(t, db); len(users) != 0 {
		t.Fatalf("expected no account after rejection, got %d", len(users))
	}
}

func TestLegacyDefaultAdminPresent(t *testing.T) {
	setBootstrapEnvironment(t, "", "")
	db := setupTestDatabase(t)

	if present, err := LegacyDefaultAdminPresent(db); err != nil || present {
		t.Fatalf("empty database: present=%v err=%v", present, err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	legacy := model.User{ID: uuid.NewString(), Username: "admin", PasswordHash: string(hash), Role: "institution_representative"}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy admin: %v", err)
	}
	if present, err := LegacyDefaultAdminPresent(db); err != nil || !present {
		t.Fatalf("legacy admin/admin: present=%v err=%v", present, err)
	}

	remediated, err := bcrypt.GenerateFromPassword([]byte("a-rotated-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&legacy).Update("password_hash", string(remediated)).Error; err != nil {
		t.Fatal(err)
	}
	if present, err := LegacyDefaultAdminPresent(db); err != nil || present {
		t.Fatalf("remediated admin: present=%v err=%v", present, err)
	}
}
