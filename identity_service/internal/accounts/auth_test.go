package accounts

import (
	"context"
	"testing"
	"time"

	"identity_service/internal/model"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func (e *testEnv) userWithPassword(t *testing.T, username, role, password string) model.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{ID: uuid.NewString(), InstitutionID: testInstitution, Username: username, Role: role, PasswordHash: string(hash)}
	if role == RoleStudent {
		user.StudentID = "031" + uuid.NewString()[:5] // every student has a student ID
	}
	if err := e.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func (e *testEnv) reload(t *testing.T, id string) model.User {
	t.Helper()
	var user model.User
	if err := e.db.First(&user, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func TestAuthenticatePausesAfterRepeatedFailures(t *testing.T) {
	env := newTestEnv(t)
	user := env.userWithPassword(t, "alice@uni.example", RoleStudent, "right-password")

	for i := 1; i < freeLoginFailures; i++ {
		_, err := env.svc.Authenticate("alice@uni.example", "wrong")
		assertCode(t, err, "INVALID_CREDENTIALS")
	}
	if got := env.reload(t, user.ID); got.LockedUntil != nil {
		t.Fatalf("paused after only %d failures", got.FailedLogins)
	}

	_, err := env.svc.Authenticate("alice@uni.example", "wrong")
	assertCode(t, err, "INVALID_CREDENTIALS")
	paused := env.reload(t, user.ID)
	if paused.LockedUntil == nil || !paused.LockedUntil.Equal(env.clock.Add(firstLockout)) {
		t.Fatalf("expected a %s pause, got %v", firstLockout, paused.LockedUntil)
	}

	// While paused even the right password is refused, with the same answer.
	_, err = env.svc.Authenticate("alice@uni.example", "right-password")
	assertCode(t, err, "INVALID_CREDENTIALS")

	// Each further failure doubles the pause.
	env.advance(firstLockout + time.Second)
	_, err = env.svc.Authenticate("alice@uni.example", "wrong")
	assertCode(t, err, "INVALID_CREDENTIALS")
	if got := env.reload(t, user.ID); !got.LockedUntil.Equal(env.clock.Add(2 * firstLockout)) {
		t.Fatalf("second pause = %v, want %s", got.LockedUntil, 2*firstLockout)
	}

	env.advance(2*firstLockout + time.Second)
	if _, err := env.svc.Authenticate("Alice@Uni.Example", "right-password"); err != nil {
		t.Fatalf("right password after the pause: %v", err)
	}
	if got := env.reload(t, user.ID); got.FailedLogins != 0 || got.LockedUntil != nil {
		t.Fatalf("a success must reset the backoff: %+v", got)
	}
}

func TestAuthenticatePauseIsCapped(t *testing.T) {
	env := newTestEnv(t)
	user := env.userWithPassword(t, "alice@uni.example", RoleStudent, "right-password")
	if err := env.db.Model(&model.User{}).Where("id = ?", user.ID).Update("failed_logins", 40).Error; err != nil {
		t.Fatal(err)
	}
	_, err := env.svc.Authenticate("alice@uni.example", "wrong")
	assertCode(t, err, "INVALID_CREDENTIALS")
	if got := env.reload(t, user.ID); !got.LockedUntil.Equal(env.clock.Add(maxLockout)) {
		t.Fatalf("pause = %v, want the %s cap", got.LockedUntil, maxLockout)
	}
}

func TestAuthenticateGivesOneAnswerForEveryFailure(t *testing.T) {
	env := newTestEnv(t)
	env.userWithPassword(t, "alice@uni.example", RoleStudent, "right-password")
	invited := model.User{ID: uuid.NewString(), InstitutionID: testInstitution, Username: "prof@uni.example", Role: RoleInstructor}
	if err := env.db.Create(&invited).Error; err != nil {
		t.Fatal(err)
	}

	var messages []string
	for _, attempt := range [][2]string{{"nobody@uni.example", "x"}, {"alice@uni.example", "wrong"}, {"prof@uni.example", "anything"}} {
		_, err := env.svc.Authenticate(attempt[0], attempt[1])
		assertCode(t, err, "INVALID_CREDENTIALS")
		messages = append(messages, err.(*Error).Message)
	}
	if messages[0] != messages[1] || messages[1] != messages[2] {
		t.Fatalf("failures must be indistinguishable: %q", messages)
	}
}

func TestPasswordResetDoesNotRevealAccounts(t *testing.T) {
	env := newTestEnv(t)
	env.userWithPassword(t, "alice@uni.example", RoleStudent, "old-password")

	unknown, err := env.svc.RequestPasswordReset(context.Background(), "nobody@uni.example")
	if err != nil {
		t.Fatal(err)
	}
	known, err := env.svc.RequestPasswordReset(context.Background(), "Alice@Uni.Example")
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Message != known.Message {
		t.Fatalf("replies differ: %q vs %q", unknown.Message, known.Message)
	}
	if len(env.mailer.sent) != 1 || env.mailer.sent[0].To != "alice@uni.example" {
		t.Fatalf("expected one email to the account owner, got %+v", env.mailer.sent)
	}
	_, err = env.svc.RequestPasswordReset(context.Background(), "not-an-email")
	assertCode(t, err, "INVALID_REQUEST")
}

func TestPasswordResetSetsPasswordOnceAndLiftsPause(t *testing.T) {
	env := newTestEnv(t)
	user := env.userWithPassword(t, "alice@uni.example", RoleStudent, "old-password")
	locked := env.clock.Add(maxLockout)
	if err := env.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{"failed_logins": 9, "locked_until": locked}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := env.svc.RequestPasswordReset(context.Background(), "alice@uni.example"); err != nil {
		t.Fatal(err)
	}
	link := env.lastLinkToken(t)
	result, err := env.svc.CompleteActivation(link, "brand-new-password")
	if err != nil || result.Role != RoleStudent {
		t.Fatalf("reset: %+v, %v", result, err)
	}
	if _, err := env.svc.Authenticate("alice@uni.example", "brand-new-password"); err != nil {
		t.Fatalf("new password must work immediately: %v", err)
	}
	if got := env.reload(t, user.ID); got.PasswordChangedAt == nil {
		t.Fatal("password change time was not recorded")
	}
	_, err = env.svc.CompleteActivation(link, "another-password")
	assertCode(t, err, "NOT_FOUND")

	env.advance(passwordResetTTL + resendInterval)
	if _, err := env.svc.RequestPasswordReset(context.Background(), "alice@uni.example"); err != nil {
		t.Fatal(err)
	}
	expired := env.lastLinkToken(t)
	env.advance(passwordResetTTL + time.Second)
	_, err = env.svc.CompleteActivation(expired, "late-password")
	assertCode(t, err, "NOT_FOUND")
}
