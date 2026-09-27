// Package reviews handles grade review requests and replies (SRS 2.8,
// 2.9, roadmap 3.4). Requests are accepted only while a grading is open;
// the instructor of a grading is the one who posted it.
package reviews

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"clearsky/contracts/messages"
	"clearsky/contracts/rpc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

const maxMessage = 2000

// Reply actions (SRS 2.9 mock-up: Total accept / Partial accept / Reject).
var validActions = map[string]bool{"total_accept": true, "partial_accept": true, "reject": true}

type Service struct{ DB *pgxpool.Pool }

func storeUnavailable(ctx context.Context, err error) error {
	slog.ErrorContext(ctx, "reviews store", "error", err)
	return rpc.Unavailable("Review requests are temporarily unavailable")
}

// Request is a review request with its course.
type Request struct {
	ID             string     `json:"id"`
	GradingID      string     `json:"grading_id"`
	CourseCode     string     `json:"course_code"`
	CourseTitle    string     `json:"course_title"`
	Period         string     `json:"period"`
	GradingState   string     `json:"grading_state"`
	StudentID      string     `json:"student_id"`
	StudentDisplay string     `json:"student_display"`
	Message        string     `json:"message"`
	Status         string     `json:"status"`
	ReplyAction    *string    `json:"reply_action"`
	ReplyMessage   *string    `json:"reply_message"`
	CreatedAt      time.Time  `json:"created_at"`
	RepliedAt      *time.Time `json:"replied_at"`
}

const requestColumns = `r.id, r.grading_id, g.course_code, g.course_title, g.period, g.state, r.student_id,
	r.student_display, r.message, r.status, r.reply_action, r.reply_message, r.created_at, r.replied_at`

func scanRequest(row pgx.Row) (Request, error) {
	var r Request
	err := row.Scan(&r.ID, &r.GradingID, &r.CourseCode, &r.CourseTitle, &r.Period, &r.GradingState, &r.StudentID,
		&r.StudentDisplay, &r.Message, &r.Status, &r.ReplyAction, &r.ReplyMessage, &r.CreatedAt, &r.RepliedAt)
	return r, err
}

func (s *Service) list(ctx context.Context, where string, args ...any) ([]Request, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+requestColumns+` FROM review_requests r
		JOIN gradings g ON g.grading_id = r.grading_id WHERE `+where+
		` ORDER BY (r.status = 'pending') DESC, r.created_at DESC`, args...)
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	defer rows.Close()
	list := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, storeUnavailable(ctx, err)
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

func (s *Service) one(ctx context.Context, where string, args ...any) (Request, error) {
	r, err := scanRequest(s.DB.QueryRow(ctx, `SELECT `+requestColumns+` FROM review_requests r
		JOIN gradings g ON g.grading_id = r.grading_id WHERE `+where, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, rpc.Fail(rpc.CodeNotFound, "The review request was not found")
	}
	if err != nil {
		return Request{}, storeUnavailable(ctx, err)
	}
	return r, nil
}

// Student is the requesting student as verified by the gateway (which also
// checked with grades-query that the student has a grade in the grading).
type Student struct {
	InstitutionID string `json:"institution_id"`
	StudentID     string `json:"student_id"`
	UserID        string `json:"student_user_id"`
	Display       string `json:"student_display"`
}

// Create files a review request on an open grading.
func (s *Service) Create(ctx context.Context, st Student, gradingID, message string) (Request, error) {
	message = strings.TrimSpace(message)
	if message == "" || utf8.RuneCountInString(message) > maxMessage {
		return Request{}, rpc.Fail(rpc.CodeInvalidRequest, "Write a message of up to 2000 characters")
	}
	if _, err := uuid.Parse(gradingID); err != nil {
		return Request{}, rpc.Fail(rpc.CodeNotFound, "The course grading was not found")
	}
	if st.StudentID == "" {
		return Request{}, rpc.Fail(rpc.CodeForbidden, "Your account has no student ID")
	}
	var state, institution string
	err := s.DB.QueryRow(ctx, `SELECT state, institution_id FROM gradings WHERE grading_id = $1`, gradingID).Scan(&state, &institution)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && institution != st.InstitutionID) {
		return Request{}, rpc.Fail(rpc.CodeNotFound, "The course grading was not found")
	}
	if err != nil {
		return Request{}, storeUnavailable(ctx, err)
	}
	if state != messages.StateOpen {
		return Request{}, rpc.Fail(rpc.CodeConflict, "The grades are final; review requests are closed")
	}
	var id string
	err = s.DB.QueryRow(ctx, `INSERT INTO review_requests (grading_id, institution_id, student_id, student_user_id, student_display, message)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, gradingID, st.InstitutionID, st.StudentID, st.UserID, st.Display, message).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Request{}, rpc.Fail(rpc.CodeConflict, "You have already asked for a review of this course")
	}
	if err != nil {
		return Request{}, storeUnavailable(ctx, err)
	}
	return s.one(ctx, `r.id = $1`, id)
}

// StudentRequests lists a student's requests (SRS 2.7 "view review status").
func (s *Service) StudentRequests(ctx context.Context, st Student) ([]Request, error) {
	return s.list(ctx, `r.institution_id = $1 AND r.student_id = $2`, st.InstitutionID, st.StudentID)
}

// Inbox lists the requests of the instructor's gradings (SRS 2.9).
func (s *Service) Inbox(ctx context.Context, institutionID, instructorID string) ([]Request, error) {
	if _, err := uuid.Parse(instructorID); err != nil {
		return nil, rpc.Fail(rpc.CodeForbidden, "A valid instructor is required")
	}
	return s.list(ctx, `r.institution_id = $1 AND g.instructor_id = $2`, institutionID, instructorID)
}

// ForInstructor returns one request of the instructor's gradings.
func (s *Service) ForInstructor(ctx context.Context, institutionID, instructorID, requestID string) (Request, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return Request{}, rpc.Fail(rpc.CodeNotFound, "The review request was not found")
	}
	return s.one(ctx, `r.id = $1 AND r.institution_id = $2 AND g.instructor_id = $3`, requestID, institutionID, instructorID)
}

// Reply answers a pending request while the grading is open.
func (s *Service) Reply(ctx context.Context, institutionID, instructorID, requestID, action, message string) (Request, error) {
	if !validActions[action] {
		return Request{}, rpc.Fail(rpc.CodeInvalidRequest, "Choose total accept, partial accept or reject")
	}
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) > maxMessage {
		return Request{}, rpc.Fail(rpc.CodeInvalidRequest, "The reply must be at most 2000 characters")
	}
	current, err := s.ForInstructor(ctx, institutionID, instructorID, requestID)
	if err != nil {
		return Request{}, err
	}
	switch {
	case current.Status != "pending":
		return Request{}, rpc.Fail(rpc.CodeConflict, "This review request has already been answered or closed")
	case current.GradingState != messages.StateOpen:
		return Request{}, rpc.Fail(rpc.CodeConflict, "The grades are final; the request can no longer be answered")
	}
	tag, err := s.DB.Exec(ctx, `UPDATE review_requests SET status = 'answered', reply_action = $2, reply_message = $3,
		replied_by = $4, replied_at = now() WHERE id = $1 AND status = 'pending'`, requestID, action, message, instructorID)
	if err != nil {
		return Request{}, storeUnavailable(ctx, err)
	}
	if tag.RowsAffected() != 1 {
		return Request{}, rpc.Fail(rpc.CodeConflict, "This review request has already been answered or closed")
	}
	return s.ForInstructor(ctx, institutionID, instructorID, requestID)
}

// ApplyHeader stores a newer grading header; at FINAL pending requests close.
func (s *Service) ApplyHeader(ctx context.Context, h messages.GradingHeader) (bool, error) {
	if _, err := uuid.Parse(h.GradingID); err != nil || h.Version < 1 {
		return false, rpc.Fail(rpc.CodeInvalidRequest, "A valid grading header is required")
	}
	var instructor *string
	if h.InstructorID != "" {
		instructor = &h.InstructorID
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO gradings (grading_id, institution_id, course_code, course_title, period, instructor_id, state, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (grading_id) DO UPDATE SET course_title = EXCLUDED.course_title, instructor_id = EXCLUDED.instructor_id,
			state = EXCLUDED.state, version = EXCLUDED.version
		WHERE gradings.version < EXCLUDED.version`,
		h.GradingID, h.InstitutionID, h.CourseCode, h.CourseTitle, h.Period, instructor, h.State, h.Version)
	if err != nil {
		return false, storeUnavailable(ctx, err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if h.State == messages.StateFinal {
		if _, err := tx.Exec(ctx, `UPDATE review_requests SET status = 'closed' WHERE grading_id = $1 AND status = 'pending'`, h.GradingID); err != nil {
			return false, storeUnavailable(ctx, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, storeUnavailable(ctx, err)
	}
	return true, nil
}

// Versions returns the stored version per grading (for reconcile).
func (s *Service) Versions(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT grading_id::text, version FROM gradings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[string]int{}
	for rows.Next() {
		var id string
		var version int
		if err := rows.Scan(&id, &version); err != nil {
			return nil, err
		}
		versions[id] = version
	}
	return versions, rows.Err()
}

// HeaderSource lists grading headers (grades-ingest).
type HeaderSource interface {
	Headers(ctx context.Context) ([]messages.GradingHeader, error)
}

// Reconcile applies every newer header from the source.
func (s *Service) Reconcile(ctx context.Context, source HeaderSource) (int, error) {
	headers, err := source.Headers(ctx)
	if err != nil {
		return 0, err
	}
	stored, err := s.Versions(ctx)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, h := range headers {
		if stored[h.GradingID] >= h.Version {
			continue
		}
		changed, err := s.ApplyHeader(ctx, h)
		if err != nil {
			return applied, err
		}
		if changed {
			applied++
		}
	}
	return applied, nil
}

// ReconcileLoop reconciles at start and every interval; failures are retried.
func (s *Service) ReconcileLoop(ctx context.Context, source HeaderSource, interval time.Duration) error {
	for {
		if applied, err := s.Reconcile(ctx, source); err != nil {
			slog.WarnContext(ctx, "reconcile failed", "error", err)
		} else if applied > 0 {
			slog.InfoContext(ctx, "reconcile applied missed gradings", "gradings", applied)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

type request struct {
	Student
	GradingID    string `json:"grading_id"`
	Message      string `json:"message"`
	InstructorID string `json:"instructor_id"`
	RequestID    string `json:"request_id"`
	Action       string `json:"action"`
}

// Handle serves reviews.request.
func (s *Service) Handle(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
	var req request
	if err := rpc.Bind(body, &req); err != nil {
		return nil, err
	}
	switch msgType {
	case "create":
		return s.Create(ctx, req.Student, req.GradingID, req.Message)
	case "student_requests":
		return s.StudentRequests(ctx, req.Student)
	case "inbox":
		return s.Inbox(ctx, req.InstitutionID, req.InstructorID)
	case "get":
		return s.ForInstructor(ctx, req.InstitutionID, req.InstructorID, req.RequestID)
	case "reply":
		return s.Reply(ctx, req.InstitutionID, req.InstructorID, req.RequestID, req.Action, req.Message)
	default:
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown reviews operation")
	}
}

// HandleSync applies reviews.sync (grading headers from the orchestrator).
func (s *Service) HandleSync(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
	if msgType != messages.TypeGradingHeader {
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown synchronisation message")
	}
	var msg messages.HeaderMessage
	if err := rpc.Bind(body, &msg); err != nil {
		return nil, err
	}
	applied, err := s.ApplyHeader(ctx, msg.GradingHeader)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"applied": applied}, nil
}
