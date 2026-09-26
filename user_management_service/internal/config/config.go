package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"user_management_service/internal/model"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	bootstrapAdminRole           = "institution_representative"
	minBootstrapPasswordLength   = 12
	legacyDefaultAdminCredential = "admin"
)

// BootstrapAdmin is an optional first administrator. It is created only when
// both BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD are set.
type BootstrapAdmin struct {
	Username string
	Password string
}

func (a BootstrapAdmin) validate() error {
	if a.Username == "" || a.Password == "" {
		return errors.New("BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if utf8.RuneCountInString(a.Password) < minBootstrapPasswordLength {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must contain at least %d characters", minBootstrapPasswordLength)
	}
	return nil
}

// BootstrapAdminFromEnvironment returns nil when no bootstrap administrator is
// configured, and an error when the configuration is partial or too weak.
func BootstrapAdminFromEnvironment() (*BootstrapAdmin, error) {
	admin := BootstrapAdmin{
		Username: strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_USERNAME")),
		Password: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}
	if admin.Username == "" && admin.Password == "" {
		return nil, nil
	}
	if err := admin.validate(); err != nil {
		return nil, err
	}
	return &admin, nil
}

// SetupDatabase opens the DB and runs migrations. It never creates an account
// unless an explicit bootstrap administrator is configured.
func SetupDatabase() (*gorm.DB, error) {
	admin, err := BootstrapAdminFromEnvironment()
	if err != nil {
		return nil, err
	}

	// Use auth_service.db as default
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "auth_service.db"
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.New(log.Default(), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		// Never write usernames or password hashes into logs.
		ParameterizedQueries: true,
	})})
	if err != nil {
		return nil, fmt.Errorf("open user database: %w", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.StudentRosterEntry{}, &model.AccountToken{}); err != nil {
		return nil, fmt.Errorf("migrate user database: %w", err)
	}

	if admin != nil {
		created, err := EnsureBootstrapAdmin(db, *admin)
		if err != nil {
			return nil, err
		}
		if created {
			log.Println("Created the configured bootstrap administrator")
		} else {
			log.Println("Bootstrap administrator already exists; it was left unchanged")
		}
	}

	legacy, err := LegacyDefaultAdminPresent(db)
	if err != nil {
		return nil, err
	}
	if legacy {
		log.Println("SECURITY WARNING: the legacy admin/admin account still exists; change its password or delete it (see docs/auth-cutover.md)")
	}

	return db, nil
}

// EnsureBootstrapAdmin creates the administrator when its username is unused.
// An existing record is never modified, including its password and role.
func EnsureBootstrapAdmin(db *gorm.DB, admin BootstrapAdmin) (bool, error) {
	if err := admin.validate(); err != nil {
		return false, err
	}
	var existing []model.User
	if err := db.Where("username = ?", admin.Username).Limit(1).Find(&existing).Error; err != nil {
		return false, fmt.Errorf("look up bootstrap administrator: %w", err)
	}
	if len(existing) > 0 {
		return false, nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(admin.Password), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("hash bootstrap administrator password: %w", err)
	}
	user := model.User{
		ID:           uuid.NewString(),
		Username:     admin.Username,
		PasswordHash: string(hash),
		Role:         bootstrapAdminRole,
	}
	if err := db.Create(&user).Error; err != nil {
		return false, fmt.Errorf("create bootstrap administrator: %w", err)
	}
	return true, nil
}

// LegacyDefaultAdminPresent reports whether the account formerly seeded on
// every start (admin/admin) still exists. It is reported, never altered.
func LegacyDefaultAdminPresent(db *gorm.DB) (bool, error) {
	var users []model.User
	if err := db.Where("username = ?", legacyDefaultAdminCredential).Limit(1).Find(&users).Error; err != nil {
		return false, fmt.Errorf("check for legacy default administrator: %w", err)
	}
	if len(users) == 0 {
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(users[0].PasswordHash), []byte(legacyDefaultAdminCredential)) == nil, nil
}
