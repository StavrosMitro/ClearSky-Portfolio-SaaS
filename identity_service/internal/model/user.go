package model

import "time"

// User is an account. Every account belongs to one institution.
type User struct {
	ID                string `gorm:"primaryKey"`
	InstitutionID     string
	Username          string
	PasswordHash      string // empty until the owner sets a password from an emailed link
	Role              string
	StudentID         string // students only
	FullName          string
	GoogleSub         *string // stable Google account ID, once linked
	FailedLogins      int     // consecutive failures, for backoff
	LockedUntil       *time.Time
	PasswordChangedAt *time.Time
	DisabledAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// StudentRosterEntry is a (student ID, university email) pair approved by the
// secretariat of an institution. Students can only create accounts that
// match an entry.
type StudentRosterEntry struct {
	InstitutionID string `gorm:"primaryKey"`
	StudentID     string `gorm:"primaryKey"`
	Email         string
	FullName      string
	UploadedBy    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (StudentRosterEntry) TableName() string { return "student_roster" }

// Account token purposes.
const (
	TokenStudentActivation = "student_activation"
	TokenSetPassword       = "set_password"
	TokenGoogleSignup      = "google_signup"
	TokenPasswordReset     = "password_reset"
)

// AccountToken is a single-use secret delivered by email or cookie. Only its
// SHA-256 hash is stored.
type AccountToken struct {
	ID            string `gorm:"primaryKey"`
	TokenHash     string
	Purpose       string
	InstitutionID string
	Email         string
	StudentID     string
	UserID        *string
	ExpiresAt     time.Time
	UsedAt        *time.Time
	CreatedAt     time.Time
}
