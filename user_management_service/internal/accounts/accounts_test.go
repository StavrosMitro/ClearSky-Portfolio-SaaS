package accounts

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"user_management_service/internal/mail"
	"user_management_service/internal/model"
	jwtutil "user_management_service/pkg/jwt"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeMailer struct {
	sent []mail.Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, msg mail.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msg)
	return nil
}

type testEnv struct {
	svc    *Service
	db     *gorm.DB
	mailer *fakeMailer
	clock  *time.Time
}

func newTestEnv(t *testing.T) *testEnv {
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
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	env := &testEnv{db: db, mailer: &fakeMailer{}, clock: &clock}
	env.svc = &Service{DB: db, Mailer: env.mailer, AppURL: "https://clearsky.example/", Now: func() time.Time { return *env.clock }}
	return env
}

func (e *testEnv) advance(d time.Duration) { *e.clock = e.clock.Add(d) }

func (e *testEnv) roster(t *testing.T, pairs ...string) {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		if err := e.db.Create(&model.StudentRosterEntry{StudentID: pairs[i], Email: pairs[i+1]}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

var linkPattern = regexp.MustCompile(`https://clearsky\.example/activate#token=([A-Za-z0-9_-]+)`)

func (e *testEnv) lastLinkToken(t *testing.T) string {
	t.Helper()
	if len(e.mailer.sent) == 0 {
		t.Fatal("no email was sent")
	}
	match := linkPattern.FindStringSubmatch(e.mailer.sent[len(e.mailer.sent)-1].Body)
	if match == nil {
		t.Fatalf("email has no activation link: %q", e.mailer.sent[len(e.mailer.sent)-1].Body)
	}
	return match[1]
}

func (e *testEnv) users(t *testing.T) []model.User {
	t.Helper()
	var users []model.User
	if err := e.db.Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	return users
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var accountErr *Error
	if !errors.As(err, &accountErr) || accountErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestRequestStudentActivationRequiresExactRosterMatch(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example", "03100002", "bob@uni.example")

	for name, pair := range map[string][2]string{
		"unknown student ID":       {"03199999", "alice@uni.example"},
		"another student's email":  {"03100001", "bob@uni.example"},
		"email not in the roster":  {"03100001", "alice@gmail.com"},
		"student ID without zeros": {"3100001", "alice@uni.example"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := env.svc.RequestStudentActivation(context.Background(), pair[0], pair[1])
			assertCode(t, err, "FORBIDDEN")
		})
	}
	if len(env.mailer.sent) != 0 {
		t.Fatalf("mismatches must not send email, sent %d", len(env.mailer.sent))
	}

	if _, err := env.svc.RequestStudentActivation(context.Background(), " 03100001 ", "Alice@Uni.Example"); err != nil {
		t.Fatalf("matching request: %v", err)
	}
	if len(env.mailer.sent) != 1 || env.mailer.sent[0].To != "alice@uni.example" {
		t.Fatalf("expected one email to the roster address, got %+v", env.mailer.sent)
	}
	raw := env.lastLinkToken(t)
	var stored model.AccountToken
	if err := env.db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash == raw || stored.TokenHash != hashSecret(raw) || stored.StudentID != "03100001" {
		t.Fatalf("token must be stored hashed and bound to the student: %+v", stored)
	}
	if users := env.users(t); len(users) != 0 {
		t.Fatalf("no account may exist before confirmation, got %d", len(users))
	}
}

func TestStudentActivationCreatesStudentOnce(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")
	if _, err := env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example"); err != nil {
		t.Fatal(err)
	}
	raw := env.lastLinkToken(t)

	_, err := env.svc.CompleteActivation(raw, "short")
	assertCode(t, err, "INVALID_REQUEST")

	result, err := env.svc.CompleteActivation(raw, "a-good-password")
	if err != nil {
		t.Fatalf("CompleteActivation: %v", err)
	}
	if result.Role != RoleStudent || result.Username != "alice@uni.example" {
		t.Fatalf("result = %+v", result)
	}
	users := env.users(t)
	if len(users) != 1 {
		t.Fatalf("expected one account, got %d", len(users))
	}
	user := users[0]
	if user.Role != RoleStudent || user.StudentID != "03100001" || user.Username != "alice@uni.example" {
		t.Fatalf("unexpected student: %+v", user)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("a-good-password")) != nil {
		t.Fatal("password was not stored as a bcrypt hash")
	}

	_, err = env.svc.CompleteActivation(raw, "another-password")
	assertCode(t, err, "NOT_FOUND")
	_, err = env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example")
	assertCode(t, err, "CONFLICT")
}

func TestStudentActivationLinkExpires(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")
	if _, err := env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example"); err != nil {
		t.Fatal(err)
	}
	raw := env.lastLinkToken(t)
	env.advance(activationTTL + time.Minute)

	_, err := env.svc.CompleteActivation(raw, "a-good-password")
	assertCode(t, err, "NOT_FOUND")
	if users := env.users(t); len(users) != 0 {
		t.Fatalf("expired link created %d accounts", len(users))
	}
}

func TestStudentActivationThrottlesAndReplacesLinks(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")
	request := func() {
		t.Helper()
		if _, err := env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example"); err != nil {
			t.Fatal(err)
		}
	}
	request()
	first := env.lastLinkToken(t)
	request()
	if len(env.mailer.sent) != 1 {
		t.Fatalf("a repeat within %s must not send another email, sent %d", resendInterval, len(env.mailer.sent))
	}

	env.advance(resendInterval + time.Second)
	request()
	if len(env.mailer.sent) != 2 {
		t.Fatalf("expected a new email after the resend interval, sent %d", len(env.mailer.sent))
	}
	_, err := env.svc.CompleteActivation(first, "a-good-password")
	assertCode(t, err, "NOT_FOUND")
	if _, err := env.svc.CompleteActivation(env.lastLinkToken(t), "a-good-password"); err != nil {
		t.Fatalf("latest link: %v", err)
	}
}

func TestStudentActivationFailsClosedWithoutEmail(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")

	env.mailer.err = errors.New("smtp down")
	_, err := env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example")
	assertCode(t, err, "DEPENDENCY_UNAVAILABLE")
	var tokens int64
	if err := env.db.Model(&model.AccountToken{}).Count(&tokens).Error; err != nil || tokens != 0 {
		t.Fatalf("a failed email must not leave a usable link (tokens=%d, err=%v)", tokens, err)
	}

	env.svc.Mailer = nil
	_, err = env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example")
	assertCode(t, err, "DEPENDENCY_UNAVAILABLE")
}

func TestStudentActivationRejectsStudentIDAlreadyRegistered(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")
	legacy := model.User{ID: uuid.NewString(), Username: "alice-old", Role: RoleStudent, StudentID: "03100001"}
	if err := env.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	_, err := env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example")
	assertCode(t, err, "CONFLICT")
}

func TestCreateInstructorInvitesWithoutPassword(t *testing.T) {
	env := newTestEnv(t)

	invitation, err := env.svc.CreateInstructor(context.Background(), " Prof@Uni.Example ")
	if err != nil {
		t.Fatalf("CreateInstructor: %v", err)
	}
	if invitation.Email != "prof@uni.example" || invitation.Resent {
		t.Fatalf("invitation = %+v", invitation)
	}
	users := env.users(t)
	if len(users) != 1 || users[0].Role != RoleInstructor || users[0].PasswordHash != "" || users[0].StudentID != "" {
		t.Fatalf("expected one instructor without a password, got %+v", users)
	}
	first := env.lastLinkToken(t)

	env.advance(resendInterval + time.Second)
	again, err := env.svc.CreateInstructor(context.Background(), "prof@uni.example")
	if err != nil || !again.Resent || again.UserID != invitation.UserID {
		t.Fatalf("re-invite = %+v, %v", again, err)
	}
	_, err = env.svc.CompleteActivation(first, "a-good-password")
	assertCode(t, err, "NOT_FOUND")

	result, err := env.svc.CompleteActivation(env.lastLinkToken(t), "a-good-password")
	if err != nil || result.Role != RoleInstructor {
		t.Fatalf("set password: %+v, %v", result, err)
	}
	var user model.User
	if err := env.db.First(&user, "id = ?", invitation.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Role != RoleInstructor || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("a-good-password")) != nil {
		t.Fatalf("instructor password not set correctly: %+v", user)
	}

	_, err = env.svc.CreateInstructor(context.Background(), "prof@uni.example")
	assertCode(t, err, "CONFLICT")
}

func TestCreateInstructorDoesNotTouchOtherAccounts(t *testing.T) {
	env := newTestEnv(t)
	student := model.User{ID: uuid.NewString(), Username: "alice@uni.example", Role: RoleStudent, StudentID: "03100001"}
	if err := env.db.Create(&student).Error; err != nil {
		t.Fatal(err)
	}
	_, err := env.svc.CreateInstructor(context.Background(), "alice@uni.example")
	assertCode(t, err, "CONFLICT")
	var after model.User
	if err := env.db.First(&after, "id = ?", student.ID).Error; err != nil || after.Role != RoleStudent {
		t.Fatalf("existing account changed: %+v, %v", after, err)
	}
	if len(env.mailer.sent) != 0 {
		t.Fatal("no invitation may be sent for an existing account")
	}

	_, err = env.svc.CreateInstructor(context.Background(), "not-an-email")
	assertCode(t, err, "INVALID_REQUEST")
}

func TestGoogleSignupRequiresRosterAndMatchingStudentID(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")

	_, err := env.svc.BeginGoogleSignup("mallory@uni.example")
	assertCode(t, err, "FORBIDDEN")

	ticket, err := env.svc.BeginGoogleSignup("alice@uni.example")
	if err != nil || ticket == "" {
		t.Fatalf("BeginGoogleSignup: %q, %v", ticket, err)
	}

	_, err = env.svc.CompleteGoogleSignup(ticket, "03100002")
	assertCode(t, err, "FORBIDDEN")
	user, err := env.svc.CompleteGoogleSignup(ticket, "03100001")
	if err != nil {
		t.Fatalf("a typo must not consume the ticket: %v", err)
	}
	if user.Role != RoleStudent || user.StudentID != "03100001" || user.Username != "alice@uni.example" || user.PasswordHash != "" {
		t.Fatalf("unexpected student: %+v", user)
	}

	_, err = env.svc.CompleteGoogleSignup(ticket, "03100001")
	assertCode(t, err, "NOT_FOUND")
	_, err = env.svc.BeginGoogleSignup("alice@uni.example")
	assertCode(t, err, "CONFLICT")
}

func TestGoogleSignupTicketExpires(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")
	ticket, err := env.svc.BeginGoogleSignup("alice@uni.example")
	if err != nil {
		t.Fatal(err)
	}
	env.advance(googleSignupTTL + time.Second)
	_, err = env.svc.CompleteGoogleSignup(ticket, "03100001")
	assertCode(t, err, "NOT_FOUND")
}

func TestAuthorizeRepresentative(t *testing.T) {
	env := newTestEnv(t)
	rep := model.User{ID: uuid.NewString(), Username: "registrar@uni.example", Role: RoleRepresentative}
	student := model.User{ID: uuid.NewString(), Username: "alice@uni.example", Role: RoleStudent, StudentID: "03100001"}
	for _, user := range []*model.User{&rep, &student} {
		if err := env.db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	token := func(user model.User) string {
		raw, err := jwtutil.GenerateToken(user.ID, user.Username, user.Role, user.StudentID)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	_, err := env.svc.AuthorizeRepresentative("")
	assertCode(t, err, "UNAUTHENTICATED")
	_, err = env.svc.AuthorizeRepresentative("not-a-jwt")
	assertCode(t, err, "UNAUTHENTICATED")
	_, err = env.svc.AuthorizeRepresentative(token(student))
	assertCode(t, err, "FORBIDDEN")

	repToken := token(rep)
	if id, err := env.svc.AuthorizeRepresentative(repToken); err != nil || id != rep.ID {
		t.Fatalf("representative: %q, %v", id, err)
	}
	if err := env.db.Model(&rep).Update("role", RoleInstructor).Error; err != nil {
		t.Fatal(err)
	}
	_, err = env.svc.AuthorizeRepresentative(repToken)
	assertCode(t, err, "FORBIDDEN")
}

func TestEmailsNeverContainOtherStudentsData(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example", "03100002", "bob@uni.example")
	if _, err := env.svc.RequestStudentActivation(context.Background(), "03100001", "alice@uni.example"); err != nil {
		t.Fatal(err)
	}
	body := env.mailer.sent[0].Body
	if strings.Contains(body, "bob") || strings.Contains(body, "03100002") {
		t.Fatalf("email leaks another roster entry: %q", body)
	}
}
