package model

import "time"

type User struct {
	ID           string `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex"`
	PasswordHash string // empty until the owner sets a password from an emailed link
	Role         string
	StudentID    string // optional school ID for students
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StudentRosterEntry is a (student ID, university email) pair approved by the
// secretariat. Students can only create accounts that match an entry.
type StudentRosterEntry struct {
	StudentID  string `gorm:"primaryKey"`
	Email      string `gorm:"uniqueIndex;not null"`
	UploadedBy string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (StudentRosterEntry) TableName() string { return "student_roster" }

// Account token purposes.
const (
	TokenStudentActivation = "student_activation"
	TokenSetPassword       = "set_password"
	TokenGoogleSignup      = "google_signup"
)

// AccountToken is a single-use secret delivered by email or cookie. Only its
// SHA-256 hash is stored.
type AccountToken struct {
	ID        string `gorm:"primaryKey"`
	TokenHash string `gorm:"uniqueIndex;not null"`
	Purpose   string `gorm:"index;not null"`
	Email     string `gorm:"index;not null"`
	StudentID string
	UserID    string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
