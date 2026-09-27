// Package config creates the optional bootstrap administrator and reports
// the legacy admin/admin account. No account is ever created implicitly.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"identity_service/internal/model"

	"clearsky/contracts/ids"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	bootstrapAdminRole           = "institution_representative"
	minBootstrapPasswordLength   = 12
	legacyDefaultAdminCredential = "admin"
	defaultBootstrapInstitution  = "ClearSky"
)

// BootstrapAdmin is an optional first administrator. It is created only when
// both BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD are set. Its
// institution is derived from BOOTSTRAP_INSTITUTION (a name); registering the
// institution later (SRS 2.2) uses the same ID.
type BootstrapAdmin struct {
	Username      string
	Password      string
	InstitutionID string
}

func (a BootstrapAdmin) validate() error {
	if a.Username == "" || a.Password == "" {
		return errors.New("BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if utf8.RuneCountInString(a.Password) < minBootstrapPasswordLength {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must contain at least %d characters", minBootstrapPasswordLength)
	}
	if _, err := uuid.Parse(a.InstitutionID); err != nil {
		return errors.New("the bootstrap administrator needs a valid institution")
	}
	return nil
}

// BootstrapAdminFromEnvironment returns nil when no bootstrap administrator is
// configured, and an error when the configuration is partial or too weak.
func BootstrapAdminFromEnvironment() (*BootstrapAdmin, error) {
	institution := strings.TrimSpace(os.Getenv("BOOTSTRAP_INSTITUTION"))
	if institution == "" {
		institution = defaultBootstrapInstitution
	}
	admin := BootstrapAdmin{
		Username:      strings.ToLower(strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_USERNAME"))),
		Password:      os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		InstitutionID: ids.Institution(institution),
	}
	if admin.Username == "" && admin.Password == "" {
		return nil, nil
	}
	if err := admin.validate(); err != nil {
		return nil, err
	}
	return &admin, nil
}

// Bootstrap creates the configured administrator (if any) and warns about a
// legacy admin/admin account. It never modifies existing accounts.
func Bootstrap(db *gorm.DB) error {
	admin, err := BootstrapAdminFromEnvironment()
	if err != nil {
		return err
	}
	if admin != nil {
		created, err := EnsureBootstrapAdmin(db, *admin)
		if err != nil {
			return err
		}
		if created {
			slog.Info("created the configured bootstrap administrator", "institution_id", admin.InstitutionID)
		} else {
			slog.Info("bootstrap administrator already exists; it was left unchanged")
		}
	}
	legacy, err := LegacyDefaultAdminPresent(db)
	if err != nil {
		return err
	}
	if legacy {
		slog.Warn("SECURITY WARNING: the legacy admin/admin account still exists; change its password or delete it (see docs/auth-cutover.md)")
	}
	return nil
}

// EnsureBootstrapAdmin creates the administrator when its username is unused.
// An existing record is never modified, including its password and role.
func EnsureBootstrapAdmin(db *gorm.DB, admin BootstrapAdmin) (bool, error) {
	if err := admin.validate(); err != nil {
		return false, err
	}
	var existing []model.User
	if err := db.Where("LOWER(username) = ?", admin.Username).Limit(1).Find(&existing).Error; err != nil {
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
		ID:            uuid.NewString(),
		InstitutionID: admin.InstitutionID,
		Username:      admin.Username,
		PasswordHash:  string(hash),
		Role:          bootstrapAdminRole,
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
