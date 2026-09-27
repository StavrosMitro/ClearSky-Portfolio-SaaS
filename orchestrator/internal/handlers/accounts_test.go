package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func decodeCall(t *testing.T, m *fakeMessenger) map[string]any {
	t.Helper()
	if len(m.calls) != 1 || m.calls[0].key != "auth.request" {
		t.Fatalf("expected one auth.request call, got %+v", m.calls)
	}
	var body map[string]any
	if err := json.Unmarshal(m.calls[0].body, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestStudentRegistrationForwardsOnlyStudentIDAndEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"message":"Check your university email"}}`)}}
	c, w := newJSONContext(http.MethodPost, `{"student_id":"03100001","email":"alice@uni.example","role":"institution_representative","password":"x"}`)

	HandleStudentRegistration(c, m)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeCall(t, m)
	if len(body) != 3 || body["type"] != "request_student_activation" || body["student_id"] != "03100001" || body["email"] != "alice@uni.example" {
		t.Fatalf("forwarded body = %v", body)
	}
}

func TestAccountErrorsExposeUserManagementMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"error":{"code":"FORBIDDEN","message":"The student ID and email do not match the student registry","retryable":false}}`)}}
	c, w := newJSONContext(http.MethodPost, `{"student_id":"03100001","email":"alice@uni.example"}`)

	HandleStudentRegistration(c, m)

	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "do not match the student registry") || !strings.Contains(w.Body.String(), `"code":"FORBIDDEN"`) {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}

func TestGoogleSignupUsesHttpOnlyTicketAndSetsSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"token":"app-jwt","role":"student","user_id":"user-1"}}`)}}
	c, w := newJSONContext(http.MethodPost, `{"student_id":"03100001","signup_ticket":"attacker-ticket"}`)
	c.Request.AddCookie(&http.Cookie{Name: googleSignupCookie, Value: "cookie-ticket"})

	HandleGoogleSignup(c, m)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeCall(t, m)
	if body["type"] != "complete_google_signup" || body["signup_ticket"] != "cookie-ticket" || body["student_id"] != "03100001" {
		t.Fatalf("forwarded body = %v", body)
	}
	if strings.Contains(w.Body.String(), "app-jwt") {
		t.Fatal("the application token must not be returned to JavaScript")
	}
	cookies := map[string]*http.Cookie{}
	for _, cookie := range w.Result().Cookies() {
		cookies[cookie.Name] = cookie
	}
	if jwt := cookies["jwt"]; jwt == nil || jwt.Value != "app-jwt" || !jwt.HttpOnly {
		t.Fatalf("jwt cookie = %+v", jwt)
	}
	if ticket := cookies[googleSignupCookie]; ticket == nil || ticket.MaxAge >= 0 {
		t.Fatalf("signup ticket cookie must be cleared, got %+v", ticket)
	}
}

func TestGoogleSignupWithoutTicketMakesNoCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{}
	c, w := newJSONContext(http.MethodPost, `{"student_id":"03100001"}`)

	HandleGoogleSignup(c, m)

	if w.Code != http.StatusUnauthorized || len(m.calls) != 0 {
		t.Fatalf("status = %d, calls = %d", w.Code, len(m.calls))
	}
}

func TestCreateInstructorForwardsCallerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"user_id":"u1","email":"prof@uni.example","resent":false}}`)}}
	c, w := newJSONContext(http.MethodPost, `{"email":"prof@uni.example","role":"institution_representative"}`)
	c.Set("raw_jwt", "caller-jwt")

	HandleCreateInstructor(c, m)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeCall(t, m)
	if len(body) != 3 || body["type"] != "create_instructor" || body["email"] != "prof@uni.example" || body["actor_token"] != "caller-jwt" {
		t.Fatalf("forwarded body = %v", body)
	}
}

func rosterUpload(t *testing.T, filename, content string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/institution/student-roster", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Set("raw_jwt", "caller-jwt")
	return c, w
}

func TestStudentRosterUploadForwardsCSVAndCallerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"received":1,"inserted":1,"updated":0,"unchanged":0}}`)}}
	csv := "student_id,email\n03100001,alice@uni.example\n"
	c, w := rosterUpload(t, "roster.CSV", csv)

	HandleStudentRosterUpload(c, m)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeCall(t, m)
	if body["type"] != "import_student_roster" || body["csv"] != csv || body["actor_token"] != "caller-jwt" {
		t.Fatalf("forwarded body = %v", body)
	}
}

func TestStudentRosterUploadRejectsWrongTypeOrSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, upload := range map[string][2]string{
		"excel file": {"roster.xlsx", "student_id,email\n"},
		"too large":  {"roster.csv", strings.Repeat("x", maxRosterBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fakeMessenger{}
			c, w := rosterUpload(t, upload[0], upload[1])
			HandleStudentRosterUpload(c, m)
			if w.Code != http.StatusBadRequest || len(m.calls) != 0 {
				t.Fatalf("status = %d, calls = %d", w.Code, len(m.calls))
			}
		})
	}
}

func TestForgotPasswordForwardsOnlyTheEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"message":"If an account exists for this email, we sent a link to choose a new password."}}`)}}
	c, w := newJSONContext(http.MethodPost, `{"email":"alice@uni.example","role":"institution_representative"}`)

	HandleForgotPassword(c, m)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeCall(t, m)
	if len(body) != 2 || body["type"] != "request_password_reset" || body["email"] != "alice@uni.example" {
		t.Fatalf("forwarded body = %v", body)
	}
}
