package config

import (
	"testing"

	"identity_service/internal/model"
	"identity_service/internal/store"

	"clearsky/contracts/ids"
	"clearsky/contracts/pgtest"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const validPassword = "correct-horse-battery"

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open(pgtest.URL(t, store.Migrations()))
	if err != nil {
		t.Fatal(err)
	}
	db.Logger = logger.Discard
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func setEnv(t *testing.T, username, password, institution string) {
	t.Helper()
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", username)
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", password)
	t.Setenv("BOOTSTRAP_INSTITUTION", institution)
}

func users(t *testing.T, db *gorm.DB) []model.User {
	t.Helper()
	var list []model.User
	if err := db.Find(&list).Error; err != nil {
		t.Fatal(err)
	}
	return list
}

func TestBootstrapAdminFromEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, username, password string
		want, wantErr            bool
	}{
		{name: "unset"},
		{name: "username only", username: "root@example.com", wantErr: true},
		{name: "password only", password: validPassword, wantErr: true},
		{name: "short password", username: "root@example.com", password: "elevenchars", wantErr: true},
		{name: "valid", username: "Root@Example.com", password: "twelve-chars", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.username, tt.password, "NTUA")
			admin, err := BootstrapAdminFromEnvironment()
			if (err != nil) != tt.wantErr || (admin != nil) != tt.want {
				t.Fatalf("admin = %+v, err = %v", admin, err)
			}
			if admin != nil && (admin.Username != "root@example.com" || admin.InstitutionID != ids.Institution("NTUA")) {
				t.Fatalf("admin = %+v", admin)
			}
		})
	}
}

func TestBootstrapCreatesNoDefaultAccount(t *testing.T) {
	setEnv(t, "", "", "")
	db := testDB(t)
	if err := Bootstrap(db); err != nil {
		t.Fatal(err)
	}
	if list := users(t, db); len(list) != 0 {
		t.Fatalf("expected no accounts, got %d", len(list))
	}
}

func TestBootstrapCreatesAdminOnceAndNeverModifiesIt(t *testing.T) {
	setEnv(t, "root@example.com", validPassword, "NTUA")
	db := testDB(t)
	if err := Bootstrap(db); err != nil {
		t.Fatal(err)
	}
	list := users(t, db)
	if len(list) != 1 || list[0].Role != "institution_representative" || list[0].InstitutionID != ids.Institution("NTUA") {
		t.Fatalf("bootstrap = %+v", list)
	}
	if _, err := uuid.Parse(list[0].ID); err != nil || bcrypt.CompareHashAndPassword([]byte(list[0].PasswordHash), []byte(validPassword)) != nil {
		t.Fatal("bootstrap admin needs a UUID and a bcrypt hash")
	}
	setEnv(t, "root@example.com", "a-different-password", "NTUA")
	if err := Bootstrap(db); err != nil {
		t.Fatal(err)
	}
	if again := users(t, db); len(again) != 1 || again[0].PasswordHash != list[0].PasswordHash {
		t.Fatal("an existing administrator must not be modified")
	}
}

func TestBootstrapRejectsInvalidConfiguration(t *testing.T) {
	setEnv(t, "root@example.com", "short", "NTUA")
	if err := Bootstrap(testDB(t)); err == nil {
		t.Fatal("a weak bootstrap password must stop startup")
	}
}

func TestLegacyDefaultAdminPresent(t *testing.T) {
	db := testDB(t)
	if present, err := LegacyDefaultAdminPresent(db); err != nil || present {
		t.Fatalf("empty: %v %v", present, err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
	legacy := model.User{ID: uuid.NewString(), InstitutionID: ids.Institution("NTUA"), Username: "admin", PasswordHash: string(hash), Role: "institution_representative"}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if present, err := LegacyDefaultAdminPresent(db); err != nil || !present {
		t.Fatalf("legacy admin: %v %v", present, err)
	}
}
