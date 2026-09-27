package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"identity_service/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	// Failures allowed before sign-in pauses; each further failure doubles
	// the pause, up to maxLockout.
	freeLoginFailures = 5
	firstLockout      = time.Minute
	maxLockout        = 15 * time.Minute
	passwordResetTTL  = time.Hour
)

// errBadCredentials is returned for every failed sign-in, whatever the
// reason, so the response does not reveal whether an account exists or is
// paused.
var errBadCredentials = fail("INVALID_CREDENTIALS", "Invalid credentials. After repeated failures, sign-in pauses for a few minutes.")

// dummyHash lets unknown usernames cost the same bcrypt work as real ones.
var dummyHash = func() []byte {
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

// Authenticate checks a username and password. Consecutive failures pause
// sign-in for the account with exponential backoff; a success resets it.
func (s *Service) Authenticate(username, password string) (model.User, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return model.User{}, fail("INVALID_REQUEST", "Username and password are required")
	}
	user, found, err := s.findLoginUser(username)
	if err != nil {
		return model.User{}, err
	}
	now := s.now()
	if !found {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return model.User{}, errBadCredentials
	}
	if user.DisabledAt != nil || (user.LockedUntil != nil && user.LockedUntil.After(now)) {
		// Disabled or paused: same work, never accepted, even with the right password.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return model.User{}, errBadCredentials
	}
	if user.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		if err := s.recordLoginFailure(user, now); err != nil {
			return model.User{}, err
		}
		return model.User{}, errBadCredentials
	}
	if user.FailedLogins != 0 || user.LockedUntil != nil {
		if err := s.DB.Model(&model.User{}).Where("id = ?", user.ID).
			Updates(map[string]any{"failed_logins": 0, "locked_until": nil}).Error; err != nil {
			return model.User{}, storeUnavailable(err)
		}
	}
	return user, nil
}

func (s *Service) recordLoginFailure(user model.User, now time.Time) error {
	failures := user.FailedLogins + 1
	updates := map[string]any{"failed_logins": failures}
	if failures >= freeLoginFailures {
		pause := firstLockout << (failures - freeLoginFailures)
		if pause > maxLockout || pause <= 0 {
			pause = maxLockout
		}
		updates["locked_until"] = now.Add(pause)
	}
	if err := s.DB.Model(&model.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
		return storeUnavailable(err)
	}
	return nil
}

// findLoginUser matches the username exactly, then as a lower-case email,
// because new accounts store university emails in lower case.
func (s *Service) findLoginUser(username string) (model.User, bool, error) {
	username = strings.TrimSpace(username)
	candidates := []string{username}
	if lower := strings.ToLower(username); lower != username && strings.Contains(username, "@") {
		candidates = append(candidates, lower)
	}
	for _, candidate := range candidates {
		user, found, err := findOne[model.User](s.DB, "username = ?", candidate)
		if err != nil || found {
			return user, found, err
		}
	}
	return model.User{}, false, nil
}

// ── Password reset ──────────────────────────────────────────────────────────

const passwordResetMessage = "If an account exists for this email, we sent a link to choose a new password."

// RequestPasswordReset emails a single-use link. The reply is identical
// whether or not the account exists, so it cannot be used to find accounts.
func (s *Service) RequestPasswordReset(ctx context.Context, rawEmail string) (ActivationRequested, error) {
	email, ok := NormalizeEmail(rawEmail)
	if !ok {
		return ActivationRequested{}, fail("INVALID_REQUEST", "A valid email is required")
	}
	if s.Mailer == nil {
		return ActivationRequested{}, errEmailDisabled
	}
	user, found, err := findOne[model.User](s.DB, "LOWER(username) = ?", email)
	if err != nil {
		return ActivationRequested{}, err
	}
	if !found || user.DisabledAt != nil {
		return ActivationRequested{Message: passwordResetMessage}, nil
	}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		throttled, err := s.replacePendingTokens(tx, model.TokenPasswordReset, email)
		if err != nil || throttled {
			return err
		}
		raw, err := s.issueToken(tx, model.TokenPasswordReset, user.InstitutionID, email, "", user.ID, passwordResetTTL)
		if err != nil {
			return err
		}
		return s.send(ctx, Message{InstitutionID: user.InstitutionID, Template: "password_reset", To: email,
			Subject: "Reset your ClearSky password"}, fmt.Sprintf(
			"Hello,\n\nOpen this link within an hour to choose a new ClearSky password:\n%s\n\n"+
				"If you did not ask for this, ignore this email; your password stays the same.\n",
			s.link("/activate", raw)))
	})
	if err != nil {
		return ActivationRequested{}, err
	}
	return ActivationRequested{Message: passwordResetMessage}, nil
}

// GoogleResult is the outcome of a verified Google sign-in: either an
// existing account, or a signup ticket for a registry student's first visit.
type GoogleResult struct {
	User         *model.User
	SignupTicket string
}

// GoogleSignIn handles a Google identity that Google auth already verified
// (email ownership, allowed domain). The stored role is always used; Google
// never creates or changes roles. The Google account's stable subject is
// linked on first use.
func (s *Service) GoogleSignIn(rawEmail, subject string) (GoogleResult, error) {
	email, ok := NormalizeEmail(rawEmail)
	if !ok {
		return GoogleResult{}, fail("INVALID_REQUEST", "A valid verified email is required")
	}
	user, found, err := findOne[model.User](s.DB, "LOWER(username) = ?", email)
	if err != nil {
		return GoogleResult{}, err
	}
	if !found {
		ticket, err := s.BeginGoogleSignup(email)
		if err != nil {
			return GoogleResult{}, err
		}
		return GoogleResult{SignupTicket: ticket}, nil
	}
	if user.DisabledAt != nil {
		return GoogleResult{}, fail("FORBIDDEN", "This account is disabled")
	}
	if subject != "" {
		switch {
		case user.GoogleSub == nil:
			if err := s.DB.Model(&model.User{}).Where("id = ? AND google_sub IS NULL", user.ID).Update("google_sub", subject).Error; err != nil {
				return GoogleResult{}, storeUnavailable(err)
			}
			user.GoogleSub = &subject
		case *user.GoogleSub != subject:
			// Same email, different Google account: refuse rather than merge.
			return GoogleResult{}, fail("FORBIDDEN", "This email is linked to a different Google account")
		}
	}
	return GoogleResult{User: &user}, nil
}

// ChangePassword replaces the password of a signed-in user after checking the
// current one (with the same backoff as sign-in).
func (s *Service) ChangePassword(username, oldPassword, newPassword string) error {
	if len([]rune(newPassword)) < minPasswordLength || len(newPassword) > maxPasswordBytes {
		return fail("INVALID_REQUEST", "The new password must contain 8 to 72 characters")
	}
	user, err := s.Authenticate(username, oldPassword)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fail("INTERNAL_ERROR", "Could not secure the password")
	}
	if err := s.DB.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
		"password_hash": string(hash), "password_changed_at": s.now(),
	}).Error; err != nil {
		return storeUnavailable(err)
	}
	return nil
}
