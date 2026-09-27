package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Platform bundles what the flows need.
type Platform struct {
	Config
	Mail Mailpit
}

// Session opens a signed-out session.
func (p Platform) Session(name string) *Session { return NewSession(p.Config, name) }

// SignIn logs in, or returns the error.
func (p Platform) SignIn(ctx context.Context, username, password string) (*Session, error) {
	s := p.Session(username)
	if err := s.Login(ctx, username, password); err != nil {
		return nil, err
	}
	return s, nil
}

// Institution is GET /institution.
type Institution struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Credits int    `json:"credits"`
}

// EnsureInstitution registers the representative's institution (idempotent).
func EnsureInstitution(ctx context.Context, rep *Session, name, contactEmail string) (Institution, error) {
	var inst Institution
	if err := rep.JSON(ctx, http.MethodGet, "/institution", nil, &inst); err == nil {
		return inst, nil
	} else if !Is(err, http.StatusNotFound) {
		return inst, err
	}
	err := rep.JSON(ctx, http.MethodPost, "/registration", map[string]string{"name": name, "contact_email": contactEmail}, &inst)
	return inst, err
}

// Purchase buys credits and returns the new balance.
func Purchase(ctx context.Context, rep *Session, amount int) (int, error) {
	if err := rep.JSON(ctx, http.MethodPatch, "/purchase", map[string]int{"amount": amount}, nil); err != nil {
		return 0, err
	}
	var inst Institution
	err := rep.JSON(ctx, http.MethodGet, "/institution", nil, &inst)
	return inst.Credits, err
}

// RosterEntry is one line of the secretariat's student registry.
type RosterEntry struct{ StudentID, Email, Name string }

// UploadRoster imports the registry as CSV.
func UploadRoster(ctx context.Context, rep *Session, entries []RosterEntry) error {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"student_id", "email", "name"})
	for _, e := range entries {
		_ = w.Write([]string{e.StudentID, e.Email, e.Name})
	}
	w.Flush()
	return rep.Upload(ctx, "/institution/student-roster", nil, "file", "roster.csv", []byte(b.String()), nil)
}

// Instructor signs in an instructor, first inviting and activating the
// account through the emailed link when it does not exist yet.
func (p Platform) Instructor(ctx context.Context, rep *Session, email, password string) (*Session, error) {
	if s, err := p.SignIn(ctx, email, password); err == nil {
		return s, nil
	}
	since := time.Now()
	if err := rep.JSON(ctx, http.MethodPost, "/institution/instructors", map[string]string{"email": email}, nil); err != nil {
		return nil, fmt.Errorf("invite %s: %w", email, err)
	}
	if err := p.activate(ctx, email, password, since); err != nil {
		return nil, err
	}
	return p.SignIn(ctx, email, password)
}

// Student signs in a student, first registering (roster-verified) and
// activating the account when it does not exist yet.
func (p Platform) Student(ctx context.Context, studentID, email, password string) (*Session, error) {
	if s, err := p.SignIn(ctx, email, password); err == nil {
		return s, nil
	}
	since := time.Now()
	anon := p.Session(email)
	if err := anon.JSON(ctx, http.MethodPost, "/user/register", map[string]string{"student_id": studentID, "email": email}, nil); err != nil {
		return nil, fmt.Errorf("register %s: %w", studentID, err)
	}
	if err := p.activate(ctx, email, password, since); err != nil {
		return nil, err
	}
	return p.SignIn(ctx, email, password)
}

func (p Platform) activate(ctx context.Context, email, password string, since time.Time) error {
	token, err := p.Mail.LinkToken(ctx, email, since)
	if err != nil {
		return err
	}
	anon := p.Session(email)
	if err := anon.JSON(ctx, http.MethodPost, "/user/activate", map[string]string{"token": token, "password": password}, nil); err != nil {
		return fmt.Errorf("activate %s: %w", email, err)
	}
	return nil
}

// Preview is the response of a workbook upload.
type Preview struct {
	UploadID       string   `json:"upload_id"`
	GradingID      string   `json:"grading_id"`
	CourseCode     string   `json:"course_code"`
	CurrentState   string   `json:"current_state"`
	GradeCount     int      `json:"grade_count"`
	RequiresCredit bool     `json:"requires_credit"`
	CanConfirm     bool     `json:"can_confirm"`
	Problems       []string `json:"problems"`
}

// Published is the response of a confirmed upload.
type Published struct {
	GradingID    string `json:"grading_id"`
	State        string `json:"state"`
	Version      int    `json:"version"`
	StudentCount int    `json:"student_count"`
	Charged      bool   `json:"charged"`
	Synchronised bool   `json:"synchronised"`
}

// UploadGrades previews a workbook (kind "initial" or "final").
func UploadGrades(ctx context.Context, instructor *Session, kind, filename string, data []byte) (Preview, error) {
	var p Preview
	err := instructor.Upload(ctx, "/grades/uploads", map[string]string{"kind": kind}, "file", filename, data, &p)
	return p, err
}

// Confirm publishes a previewed upload.
func Confirm(ctx context.Context, instructor *Session, uploadID string) (Published, error) {
	var out Published
	err := instructor.JSON(ctx, http.MethodPost, "/grades/uploads/"+uploadID+"/confirm", nil, &out)
	return out, err
}

// Grading is a course grading as students and instructors see it.
type Grading struct {
	GradingID    string `json:"grading_id"`
	CourseCode   string `json:"course_code"`
	CourseTitle  string `json:"course_title"`
	Period       string `json:"period"`
	State        string `json:"state"`
	Version      int    `json:"version"`
	StudentCount int    `json:"student_count"`
}

// PersonalGrade is one of a student's grades.
type PersonalGrade struct {
	Grading
	Total          *float64   `json:"total"`
	QuestionScores []*float64 `json:"question_scores"`
}

// Review is a review request.
type Review struct {
	ID           string  `json:"id"`
	GradingID    string  `json:"grading_id"`
	CourseCode   string  `json:"course_code"`
	GradingState string  `json:"grading_state"`
	StudentID    string  `json:"student_id"`
	Message      string  `json:"message"`
	Status       string  `json:"status"`
	ReplyAction  *string `json:"reply_action"`
	ReplyMessage *string `json:"reply_message"`
}

// RequestReview files a review request.
func RequestReview(ctx context.Context, student *Session, gradingID, message string) (Review, error) {
	var r Review
	err := student.JSON(ctx, http.MethodPost, "/reviews", map[string]string{"grading_id": gradingID, "message": message}, &r)
	return r, err
}

// Reply answers a review request.
func Reply(ctx context.Context, instructor *Session, requestID, action, message string) (Review, error) {
	var r Review
	err := instructor.JSON(ctx, http.MethodPost, "/reviews/"+requestID+"/reply", map[string]string{"action": action, "message": message}, &r)
	return r, err
}

// Eventually retries check until it succeeds or the timeout passes (the
// read models are updated asynchronously).
func Eventually(ctx context.Context, timeout time.Duration, check func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
