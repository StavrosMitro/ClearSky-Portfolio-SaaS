// Package accounts implements account onboarding: the secretariat's student
// roster, roster-verified student registration (by email link or Google), and
// instructor invitations. User management remains the only place that creates
// accounts or decides roles.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	netmail "net/mail"
	"regexp"
	"strings"
	"time"

	"identity_service/internal/model"

	"clearsky/contracts/rpc"
	jwtutil "identity_service/pkg/jwt"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	RoleStudent        = "student"
	RoleInstructor     = "instructor"
	RoleRepresentative = "institution_representative"

	activationTTL     = 24 * time.Hour
	invitationTTL     = 72 * time.Hour
	googleSignupTTL   = 15 * time.Minute
	resendInterval    = 2 * time.Minute
	minPasswordLength = 8
	maxPasswordBytes  = 72 // bcrypt limit
)

var studentIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)

// Error is a failure whose code and message are safe to return to clients
// (the shared RPC error of the contracts module).
type Error = rpc.Error

func fail(code, message string) error { return &Error{Code: code, Message: message} }

func unavailable(message string) error {
	return &Error{Code: "DEPENDENCY_UNAVAILABLE", Message: message, Retryable: true}
}

func storeUnavailable(err error) error {
	log.Printf("user store: %v", err)
	return unavailable("User store is unavailable")
}

var (
	errInvalidLink    = fail("NOT_FOUND", "This link is invalid or has expired")
	errAccountExists  = fail("CONFLICT", "An account already exists for this student. Sign in instead.")
	errEmailDisabled  = unavailable("Email delivery is not configured")
	errRosterMismatch = fail("FORBIDDEN", "The student ID and email do not match the student registry")
)

// Message is one account email.
type Message struct {
	InstitutionID string
	Template      string // e.g. student_activation; for logs and deduplication
	To            string
	Subject       string
	Body          string
}

// Mailer delivers account emails. In production it hands them to the
// notifications service, which applies the global hourly budget.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

type Service struct {
	DB     *gorm.DB
	Mailer Mailer // nil disables every flow that sends email
	AppURL string // public front-end URL used in emailed links
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// NormalizeEmail lower-cases and validates a plain email address.
func NormalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	address, err := netmail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email || len(email) > 254 {
		return "", false
	}
	return email, true
}

// NormalizeStudentID keeps leading zeros; IDs are compared exactly.
func NormalizeStudentID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	return id, studentIDPattern.MatchString(id)
}

// AuthorizeRepresentative verifies the caller's application JWT and that the
// stored account is still an active institution representative. It returns
// the representative's user and institution.
func (s *Service) AuthorizeRepresentative(actorToken string) (string, string, error) {
	if actorToken == "" {
		return "", "", fail("UNAUTHENTICATED", "Authentication is required")
	}
	claims, err := jwtutil.ParseToken(actorToken)
	if err != nil {
		return "", "", fail("UNAUTHENTICATED", "Your session is invalid or has expired")
	}
	user, found, err := findOne[model.User](s.DB, "id = ?", claims.UserID)
	if err != nil {
		return "", "", err
	}
	if claims.Role != RoleRepresentative || !found || user.Role != RoleRepresentative || user.DisabledAt != nil {
		return "", "", fail("FORBIDDEN", "Only institution representatives can manage accounts")
	}
	return user.ID, user.InstitutionID, nil
}

// ── Student registration by roster and email link (solution A) ─────────────

type ActivationRequested struct {
	Message string `json:"message"`
}

func (s *Service) RequestStudentActivation(ctx context.Context, rawStudentID, rawEmail string) (ActivationRequested, error) {
	studentID, okID := NormalizeStudentID(rawStudentID)
	email, okEmail := NormalizeEmail(rawEmail)
	if !okID || !okEmail {
		return ActivationRequested{}, fail("INVALID_REQUEST", "A valid student ID and university email are required")
	}
	if s.Mailer == nil {
		return ActivationRequested{}, errEmailDisabled
	}
	// The matching roster entry decides the institution.
	entry, found, err := findOne[model.StudentRosterEntry](s.DB, "student_id = ? AND email = ?", studentID, email)
	if err != nil {
		return ActivationRequested{}, err
	}
	if !found {
		return ActivationRequested{}, errRosterMismatch
	}
	if exists, err := accountExists(s.DB, entry.InstitutionID, email, studentID); err != nil {
		return ActivationRequested{}, err
	} else if exists {
		return ActivationRequested{}, errAccountExists
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		throttled, err := s.replacePendingTokens(tx, model.TokenStudentActivation, email)
		if err != nil || throttled {
			return err
		}
		raw, err := s.issueToken(tx, model.TokenStudentActivation, entry.InstitutionID, email, studentID, "", activationTTL)
		if err != nil {
			return err
		}
		return s.send(ctx, Message{InstitutionID: entry.InstitutionID, Template: "student_activation", To: email,
			Subject: "Confirm your ClearSky student account"}, fmt.Sprintf(
			"Hello,\n\nA ClearSky account was requested for student ID %s.\n\n"+
				"Open this link within 24 hours to confirm your email and choose a password:\n%s\n\n"+
				"If you did not request this, ignore this email; no account will be created.\n",
			studentID, s.link("/activate", raw)))
	})
	if err != nil {
		return ActivationRequested{}, err
	}
	return ActivationRequested{Message: "Check your university email for a confirmation link"}, nil
}

type ActivationResult struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// CompleteActivation consumes an emailed link and sets the password: it
// creates a roster-verified student, finishes an instructor invitation, or
// completes a password reset.
func (s *Service) CompleteActivation(rawToken, password string) (ActivationResult, error) {
	if len([]rune(password)) < minPasswordLength || len(password) > maxPasswordBytes {
		return ActivationResult{}, fail("INVALID_REQUEST", "The password must contain 8 to 72 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return ActivationResult{}, fail("INTERNAL_ERROR", "Could not secure the password")
	}

	var result ActivationResult
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		token, err := s.consumeToken(tx, rawToken, model.TokenStudentActivation, model.TokenSetPassword, model.TokenPasswordReset)
		if err != nil {
			return err
		}
		if token.Purpose == model.TokenSetPassword || token.Purpose == model.TokenPasswordReset {
			if token.UserID == nil {
				return errInvalidLink
			}
			var user model.User
			if err := tx.Where("id = ?", *token.UserID).First(&user).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errInvalidLink
				}
				return storeUnavailable(err)
			}
			// A new password also lifts any sign-in pause.
			if err := tx.Model(&user).Updates(map[string]any{
				"password_hash": string(hash), "failed_logins": 0, "locked_until": nil, "password_changed_at": s.now(),
			}).Error; err != nil {
				return storeUnavailable(err)
			}
			result = ActivationResult{Username: user.Username, Role: user.Role}
			return nil
		}

		entry, found, err := findOne[model.StudentRosterEntry](tx, "institution_id = ? AND student_id = ?", token.InstitutionID, token.StudentID)
		if err != nil {
			return err
		}
		if !found || entry.Email != token.Email {
			return fail("FORBIDDEN", "The student registry changed; request a new confirmation email")
		}
		user, err := createStudent(tx, token.InstitutionID, token.Email, token.StudentID, string(hash), entry.FullName)
		if err != nil {
			return err
		}
		result = ActivationResult{Username: user.Username, Role: user.Role}
		return nil
	})
	return result, err
}

// ── Student registration through Google Workspace (solution B) ─────────────

// BeginGoogleSignup is called for a verified university Google email that has
// no account yet. It returns a short-lived ticket for the student-ID step.
func (s *Service) BeginGoogleSignup(rawEmail string) (string, error) {
	email, ok := NormalizeEmail(rawEmail)
	if !ok {
		return "", fail("INVALID_REQUEST", "A valid verified email is required")
	}
	var entries []model.StudentRosterEntry
	if err := s.DB.Where("email = ?", email).Limit(2).Find(&entries).Error; err != nil {
		return "", storeUnavailable(err)
	}
	switch len(entries) {
	case 0:
		return "", fail("FORBIDDEN", "This Google account is not in the student registry")
	case 2:
		return "", fail("FORBIDDEN", "This email is registered at more than one institution; sign up with your student ID instead")
	}
	entry := entries[0]
	if exists, err := accountExists(s.DB, entry.InstitutionID, email, entry.StudentID); err != nil {
		return "", err
	} else if exists {
		return "", errAccountExists
	}
	var ticket string
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		if err := tx.Where("purpose = ? AND email = ? AND used_at IS NULL", model.TokenGoogleSignup, email).Delete(&model.AccountToken{}).Error; err != nil {
			return storeUnavailable(err)
		}
		ticket, err = s.issueToken(tx, model.TokenGoogleSignup, entry.InstitutionID, email, "", "", googleSignupTTL)
		return err
	})
	return ticket, err
}

// CompleteGoogleSignup creates the student once the typed student ID matches
// the roster entry of the Google-verified email.
func (s *Service) CompleteGoogleSignup(rawTicket, rawStudentID string) (model.User, error) {
	studentID, ok := NormalizeStudentID(rawStudentID)
	if !ok {
		return model.User{}, fail("INVALID_REQUEST", "A valid student ID is required")
	}
	token, err := s.findToken(s.DB, rawTicket, model.TokenGoogleSignup)
	if err != nil {
		return model.User{}, fail("NOT_FOUND", "Your Google sign-up session has expired; sign in with Google again")
	}
	entry, found, err := findOne[model.StudentRosterEntry](s.DB, "institution_id = ? AND email = ?", token.InstitutionID, token.Email)
	if err != nil {
		return model.User{}, err
	}
	// A mismatch keeps the ticket, so a typo can be corrected.
	if !found || entry.StudentID != studentID {
		return model.User{}, fail("FORBIDDEN", "The student ID does not match the student registry for your university email")
	}

	var user model.User
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := s.consumeToken(tx, rawTicket, model.TokenGoogleSignup); err != nil {
			return err
		}
		user, err = createStudent(tx, token.InstitutionID, token.Email, studentID, "", entry.FullName)
		return err
	})
	return user, err
}

// ── Instructor invitations ──────────────────────────────────────────────────

type InstructorInvitation struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Resent bool   `json:"resent"`
}

// CreateInstructor creates an instructor without a password and emails a
// link to set one. Repeating it for an instructor who has not set a password
// yet re-sends the invitation.
func (s *Service) CreateInstructor(ctx context.Context, institutionID, rawEmail string) (InstructorInvitation, error) {
	email, ok := NormalizeEmail(rawEmail)
	if !ok {
		return InstructorInvitation{}, fail("INVALID_REQUEST", "A valid instructor email is required")
	}
	if s.Mailer == nil {
		return InstructorInvitation{}, errEmailDisabled
	}
	var invitation InstructorInvitation
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var existing []model.User
		if err := tx.Where("LOWER(username) = ?", email).Limit(1).Find(&existing).Error; err != nil {
			return storeUnavailable(err)
		}
		var user model.User
		if len(existing) > 0 {
			user = existing[0]
			if user.Role != RoleInstructor || user.PasswordHash != "" || user.InstitutionID != institutionID {
				return fail("CONFLICT", "An account with this email already exists")
			}
			invitation.Resent = true
			throttled, err := s.replacePendingTokens(tx, model.TokenSetPassword, email)
			if err != nil || throttled {
				invitation.UserID, invitation.Email = user.ID, user.Username
				return err
			}
		} else {
			user = model.User{ID: uuid.NewString(), InstitutionID: institutionID, Username: email, Role: RoleInstructor}
			if err := tx.Create(&user).Error; err != nil {
				return storeUnavailable(err)
			}
		}
		invitation.UserID, invitation.Email = user.ID, user.Username

		raw, err := s.issueToken(tx, model.TokenSetPassword, institutionID, email, "", user.ID, invitationTTL)
		if err != nil {
			return err
		}
		return s.send(ctx, Message{InstitutionID: institutionID, Template: "instructor_invitation", To: email,
			Subject: "Your ClearSky instructor account"}, fmt.Sprintf(
			"Hello,\n\nThe secretariat created a ClearSky instructor account for %s.\n\n"+
				"Open this link within 72 hours to choose your password:\n%s\n\n"+
				"You can also sign in with your university Google account.\n",
			email, s.link("/activate", raw)))
	})
	if err != nil {
		return InstructorInvitation{}, err
	}
	return invitation, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func (s *Service) link(path, token string) string {
	// The token travels in the fragment, so it never reaches server logs.
	return strings.TrimRight(s.AppURL, "/") + path + "#token=" + token
}

// send hands the email over for delivery. If that fails, the caller's
// transaction rolls back, so no link exists that nobody received.
func (s *Service) send(ctx context.Context, msg Message, body string) error {
	msg.Body = body
	if err := s.Mailer.Send(ctx, msg); err != nil {
		log.Printf("send account email: %v", err)
		return unavailable("The email could not be sent; try again later")
	}
	return nil
}

func findOne[T any](db *gorm.DB, query string, args ...any) (T, bool, error) {
	var rows []T
	if err := db.Where(query, args...).Limit(1).Find(&rows).Error; err != nil {
		var zero T
		return zero, false, storeUnavailable(err)
	}
	if len(rows) == 0 {
		var zero T
		return zero, false, nil
	}
	return rows[0], true, nil
}

// accountExists: usernames are unique everywhere; student IDs per institution.
func accountExists(db *gorm.DB, institutionID, email, studentID string) (bool, error) {
	var count int64
	if err := db.Model(&model.User{}).
		Where("LOWER(username) = ? OR (institution_id = ? AND student_id <> '' AND student_id = ?)", email, institutionID, studentID).
		Count(&count).Error; err != nil {
		return false, storeUnavailable(err)
	}
	return count > 0, nil
}

func createStudent(tx *gorm.DB, institutionID, email, studentID, passwordHash, fullName string) (model.User, error) {
	if exists, err := accountExists(tx, institutionID, email, studentID); err != nil {
		return model.User{}, err
	} else if exists {
		return model.User{}, errAccountExists
	}
	user := model.User{ID: uuid.NewString(), InstitutionID: institutionID, Username: email, PasswordHash: passwordHash,
		Role: RoleStudent, StudentID: studentID, FullName: fullName}
	if err := tx.Create(&user).Error; err != nil {
		return model.User{}, storeUnavailable(err)
	}
	return user, nil
}

func hashSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) issueToken(tx *gorm.DB, purpose, institutionID, email, studentID, userID string, ttl time.Duration) (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fail("INTERNAL_ERROR", "Could not create a secure link")
	}
	raw := base64.RawURLEncoding.EncodeToString(secret)
	now := s.now()
	var owner *string
	if userID != "" {
		owner = &userID
	}
	token := model.AccountToken{
		ID: uuid.NewString(), TokenHash: hashSecret(raw), Purpose: purpose, InstitutionID: institutionID, Email: email,
		StudentID: studentID, UserID: owner, ExpiresAt: now.Add(ttl), CreatedAt: now,
	}
	if err := tx.Create(&token).Error; err != nil {
		return "", storeUnavailable(err)
	}
	return raw, nil
}

// findToken returns a valid, unused token without consuming it.
func (s *Service) findToken(db *gorm.DB, raw string, purposes ...string) (model.AccountToken, error) {
	if raw == "" || len(raw) > 128 {
		return model.AccountToken{}, errInvalidLink
	}
	token, found, err := findOne[model.AccountToken](db, "token_hash = ? AND purpose IN ?", hashSecret(raw), purposes)
	if err != nil {
		return model.AccountToken{}, err
	}
	if !found || token.UsedAt != nil || !token.ExpiresAt.After(s.now()) {
		return model.AccountToken{}, errInvalidLink
	}
	return token, nil
}

// consumeToken marks a valid token used; concurrent use succeeds only once.
func (s *Service) consumeToken(tx *gorm.DB, raw string, purposes ...string) (model.AccountToken, error) {
	token, err := s.findToken(tx, raw, purposes...)
	if err != nil {
		return model.AccountToken{}, err
	}
	result := tx.Model(&model.AccountToken{}).Where("id = ? AND used_at IS NULL", token.ID).Update("used_at", s.now())
	if result.Error != nil {
		return model.AccountToken{}, storeUnavailable(result.Error)
	}
	if result.RowsAffected != 1 {
		return model.AccountToken{}, errInvalidLink
	}
	return token, nil
}

// replacePendingTokens deletes unused tokens of one purpose for an email, or
// reports throttled when one was issued moments ago (limits email floods).
func (s *Service) replacePendingTokens(tx *gorm.DB, purpose, email string) (bool, error) {
	var pending []model.AccountToken
	if err := tx.Where("purpose = ? AND email = ? AND used_at IS NULL", purpose, email).Find(&pending).Error; err != nil {
		return false, storeUnavailable(err)
	}
	now := s.now()
	for _, token := range pending {
		if token.ExpiresAt.After(now) && now.Sub(token.CreatedAt) < resendInterval {
			return true, nil
		}
	}
	if len(pending) > 0 {
		if err := tx.Where("purpose = ? AND email = ? AND used_at IS NULL", purpose, email).Delete(&model.AccountToken{}).Error; err != nil {
			return false, storeUnavailable(err)
		}
	}
	return false, nil
}
