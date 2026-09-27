package accounts

import (
	"testing"

	"identity_service/internal/model"

	"github.com/google/uuid"
)

func TestGoogleSignInLinksTheGoogleAccount(t *testing.T) {
	env := newTestEnv(t)
	prof := model.User{ID: uuid.NewString(), InstitutionID: testInstitution, Username: "prof@uni.example", Role: RoleInstructor}
	if err := env.db.Create(&prof).Error; err != nil {
		t.Fatal(err)
	}

	result, err := env.svc.GoogleSignIn("Prof@Uni.Example", "google-sub-1")
	if err != nil || result.User == nil || result.User.ID != prof.ID || result.User.Role != RoleInstructor {
		t.Fatalf("first sign-in = %+v, %v", result, err)
	}
	stored := env.reload(t, prof.ID)
	if stored.GoogleSub == nil || *stored.GoogleSub != "google-sub-1" {
		t.Fatalf("google subject not linked: %+v", stored.GoogleSub)
	}
	if again, err := env.svc.GoogleSignIn("prof@uni.example", "google-sub-1"); err != nil || again.User == nil {
		t.Fatalf("second sign-in = %+v, %v", again, err)
	}
	_, err = env.svc.GoogleSignIn("prof@uni.example", "another-google-account")
	assertCode(t, err, "FORBIDDEN")
}

func TestGoogleSignInStartsSignupForRosterStudents(t *testing.T) {
	env := newTestEnv(t)
	env.roster(t, "03100001", "alice@uni.example")
	result, err := env.svc.GoogleSignIn("alice@uni.example", "sub-a")
	if err != nil || result.User != nil || result.SignupTicket == "" {
		t.Fatalf("roster student = %+v, %v", result, err)
	}
	_, err = env.svc.GoogleSignIn("mallory@uni.example", "sub-m")
	assertCode(t, err, "FORBIDDEN")
}

func TestGoogleSignInRefusesDisabledAccounts(t *testing.T) {
	env := newTestEnv(t)
	disabled := env.clock.Add(-1)
	user := model.User{ID: uuid.NewString(), InstitutionID: testInstitution, Username: "gone@uni.example", Role: RoleInstructor, DisabledAt: &disabled}
	if err := env.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	_, err := env.svc.GoogleSignIn("gone@uni.example", "sub")
	assertCode(t, err, "FORBIDDEN")
}
