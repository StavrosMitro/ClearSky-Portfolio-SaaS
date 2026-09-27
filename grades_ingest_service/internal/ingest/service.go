// Package ingest owns the grades (roadmap 3.6). It parses e-sec workbooks,
// shows a preview, and on CONFIRM applies the SRS state machine
// NULL → open → final. It is the source of truth that grades-query and
// reviews are synchronised from (through the orchestrator).
package ingest

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"time"

	"clearsky/contracts/ids"
	"clearsky/contracts/messages"
	"clearsky/contracts/rpc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

const (
	KindInitial   = "initial"
	KindFinal     = "final"
	previewTTL    = time.Hour
	MaxUploadSize = 5 << 20
)

type Service struct {
	DB  *pgxpool.Pool
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func storeUnavailable(ctx context.Context, err error) error {
	slog.ErrorContext(ctx, "grades store", "error", err)
	return rpc.Unavailable("The grades store is unavailable")
}

// Preview is what the instructor confirms or cancels (SRS 2.5 mock-up).
type Preview struct {
	UploadID       string    `json:"upload_id"`
	Kind           string    `json:"kind"`
	GradingID      string    `json:"grading_id,omitempty"`
	CourseCode     string    `json:"course_code,omitempty"`
	CourseTitle    string    `json:"course_title,omitempty"`
	Period         string    `json:"period,omitempty"`
	GradeCount     int       `json:"grade_count"`
	QuestionCount  int       `json:"question_count"`
	Weights        []float64 `json:"question_weights"`
	CurrentState   string    `json:"current_state,omitempty"` // "" (none yet), open or final
	RequiresCredit bool      `json:"requires_credit"`
	Problems       []string  `json:"problems"`
	CanConfirm     bool      `json:"can_confirm"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type gradingRow struct {
	ID           string
	State        string
	Version      int
	InstructorID *string
}

func (s *Service) currentGrading(ctx context.Context, q pgx.Tx, id string, lock bool) (*gradingRow, error) {
	sql := `SELECT id, state, version, instructor_id FROM gradings WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var g gradingRow
	var err error
	if q != nil {
		err = q.QueryRow(ctx, sql, id).Scan(&g.ID, &g.State, &g.Version, &g.InstructorID)
	} else {
		err = s.DB.QueryRow(ctx, sql, id).Scan(&g.ID, &g.State, &g.Version, &g.InstructorID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// transitionProblem applies the SRS state machine to an upload.
func transitionProblem(kind, uploader string, current *gradingRow) string {
	switch {
	case current != nil && current.State == messages.StateFinal:
		return "The grades of this course and exam period are final and can no longer change"
	case kind == KindFinal && current == nil:
		return "Post the initial grades of this course and exam period first"
	case current != nil && current.InstructorID != nil && *current.InstructorID != uploader:
		return "Another instructor manages the grades of this course and exam period"
	}
	return ""
}

// CreatePreview parses a workbook and stores it until CONFIRM or CANCEL.
func (s *Service) CreatePreview(ctx context.Context, institutionID, uploader, kind, filename string, file []byte) (Preview, error) {
	if _, err := uuid.Parse(institutionID); err != nil {
		return Preview{}, rpc.Fail(rpc.CodeInvalidRequest, "A valid institution is required")
	}
	if _, err := uuid.Parse(uploader); err != nil {
		return Preview{}, rpc.Fail(rpc.CodeInvalidRequest, "A valid uploader is required")
	}
	if kind != KindInitial && kind != KindFinal {
		return Preview{}, rpc.Fail(rpc.CodeInvalidRequest, "The upload must be initial or final")
	}
	if len(file) == 0 || len(file) > MaxUploadSize {
		return Preview{}, rpc.Fail(rpc.CodeInvalidRequest, "The workbook must be smaller than 5 MiB")
	}
	parsed, err := Parse(file)
	if err != nil {
		return Preview{}, rpc.Fail(rpc.CodeInvalidRequest, "The file is not a valid .xlsx workbook")
	}

	preview := Preview{Kind: kind, Problems: parsed.Problems, ExpiresAt: s.now().Add(previewTTL),
		CourseCode: parsed.CourseCode, CourseTitle: parsed.CourseTitle, Period: parsed.Period,
		GradeCount: len(parsed.Rows), QuestionCount: len(parsed.Weights), Weights: parsed.Weights}
	if preview.Weights == nil {
		preview.Weights = []float64{}
	}
	if preview.Problems == nil {
		preview.Problems = []string{}
	}
	if parsed.CourseCode != "" && parsed.Period != "" {
		preview.GradingID = ids.Grading(institutionID, parsed.CourseCode, parsed.Period)
		current, err := s.currentGrading(ctx, nil, preview.GradingID, false)
		if err != nil {
			return Preview{}, storeUnavailable(ctx, err)
		}
		if current != nil {
			preview.CurrentState = current.State
		}
		if problem := transitionProblem(kind, uploader, current); problem != "" {
			preview.Problems = append(preview.Problems, problem)
		}
		preview.RequiresCredit = kind == KindInitial && current == nil
	}
	preview.CanConfirm = len(preview.Problems) == 0

	status := "parsed"
	if !preview.CanConfirm {
		status = "rejected"
	}
	digest := sha256.Sum256(file)
	parsedJSON, _ := json.Marshal(parsed)
	problemsJSON, _ := json.Marshal(preview.Problems)
	var gradingID *string
	if preview.GradingID != "" {
		gradingID = &preview.GradingID
	}
	err = s.DB.QueryRow(ctx, `INSERT INTO uploads (institution_id, kind, status, uploaded_by, filename, file, file_sha256,
			grading_id, course_code, course_title, period, parsed, problems, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), $12, $13, $14) RETURNING id`,
		institutionID, kind, status, uploader, filename, file, hex.EncodeToString(digest[:]), gradingID,
		parsed.CourseCode, parsed.CourseTitle, parsed.Period, parsedJSON, problemsJSON, preview.ExpiresAt).Scan(&preview.UploadID)
	if err != nil {
		return Preview{}, storeUnavailable(ctx, err)
	}
	return preview, nil
}

type upload struct {
	ID, InstitutionID, Kind, Status, UploadedBy string
	GradingID                                   *string
	Parsed                                      Parsed
	ExpiresAt                                   time.Time
}

func (s *Service) loadUpload(ctx context.Context, tx pgx.Tx, institutionID, uploader, uploadID string) (*upload, error) {
	if _, err := uuid.Parse(uploadID); err != nil {
		return nil, rpc.Fail(rpc.CodeNotFound, "The upload was not found")
	}
	var u upload
	var parsed []byte
	err := tx.QueryRow(ctx, `SELECT id, institution_id, kind, status, uploaded_by, grading_id, parsed, expires_at
		FROM uploads WHERE id = $1 FOR UPDATE`, uploadID).
		Scan(&u.ID, &u.InstitutionID, &u.Kind, &u.Status, &u.UploadedBy, &u.GradingID, &parsed, &u.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (u.InstitutionID != institutionID || u.UploadedBy != uploader)) {
		return nil, rpc.Fail(rpc.CodeNotFound, "The upload was not found")
	}
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	if len(parsed) > 0 {
		if err := json.Unmarshal(parsed, &u.Parsed); err != nil {
			return nil, storeUnavailable(ctx, err)
		}
	}
	return &u, nil
}

// Confirm publishes an upload: it creates or changes the grading and
// returns the new snapshot for synchronisation. Confirming an already
// confirmed upload returns the current snapshot (safe to retry).
func (s *Service) Confirm(ctx context.Context, institutionID, uploader, uploadID string) (messages.GradingSnapshot, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	u, err := s.loadUpload(ctx, tx, institutionID, uploader, uploadID)
	if err != nil {
		return messages.GradingSnapshot{}, err
	}
	switch {
	case u.Status == "confirmed" && u.GradingID != nil:
		_ = tx.Rollback(ctx)
		return s.Snapshot(ctx, *u.GradingID)
	case u.Status != "parsed":
		return messages.GradingSnapshot{}, rpc.Fail(rpc.CodeConflict, "This upload cannot be confirmed; upload the workbook again")
	case !u.ExpiresAt.After(s.now()):
		return messages.GradingSnapshot{}, rpc.Fail(rpc.CodeConflict, "The preview has expired; upload the workbook again")
	}

	gradingID := *u.GradingID
	current, err := s.currentGrading(ctx, tx, gradingID, true)
	if err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	if problem := transitionProblem(u.Kind, uploader, current); problem != "" {
		return messages.GradingSnapshot{}, rpc.Fail(rpc.CodeConflict, problem)
	}
	now := s.now()
	p := u.Parsed
	switch {
	case current == nil:
		_, err = tx.Exec(ctx, `INSERT INTO gradings (id, institution_id, course_code, course_title, period, state, version,
				instructor_id, grading_scale, question_weights, opened_at)
			VALUES ($1, $2, $3, $4, $5, 'open', 1, $6, NULLIF($7, ''), $8, $9)`,
			gradingID, institutionID, p.CourseCode, p.CourseTitle, p.Period, uploader, p.GradingScale, p.Weights, now)
	case u.Kind == KindInitial:
		_, err = tx.Exec(ctx, `UPDATE gradings SET course_title = $2, grading_scale = NULLIF($3, ''), question_weights = $4,
			version = version + 1, instructor_id = COALESCE(instructor_id, $5), updated_at = now() WHERE id = $1`,
			gradingID, p.CourseTitle, p.GradingScale, p.Weights, uploader)
	default:
		_, err = tx.Exec(ctx, `UPDATE gradings SET state = 'final', finalized_at = $2, course_title = $3,
			grading_scale = NULLIF($4, ''), question_weights = $5, version = version + 1,
			instructor_id = COALESCE(instructor_id, $6), updated_at = now() WHERE id = $1`,
			gradingID, now, p.CourseTitle, p.GradingScale, p.Weights, uploader)
	}
	if err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM grades WHERE grading_id = $1`, gradingID); err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	rows := make([][]any, 0, len(p.Rows))
	for _, r := range p.Rows {
		rows = append(rows, []any{gradingID, r.StudentID, nullable(r.StudentName), r.Total, r.QuestionScores, u.ID})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"grades"},
		[]string{"grading_id", "student_id", "student_name", "total", "question_scores", "upload_id"}, pgx.CopyFromRows(rows)); err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE uploads SET status = 'confirmed', confirmed_at = $2 WHERE id = $1`, u.ID, now); err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	return s.Snapshot(ctx, gradingID)
}

// UploadStatus tells the gateway, before CONFIRM, whether publishing this
// upload creates a new grading (and so needs a credit).
type UploadStatus struct {
	UploadID       string `json:"upload_id"`
	Status         string `json:"status"`
	Kind           string `json:"kind"`
	GradingID      string `json:"grading_id,omitempty"`
	CanConfirm     bool   `json:"can_confirm"`
	RequiresCredit bool   `json:"requires_credit"`
}

func (s *Service) Status(ctx context.Context, institutionID, uploader, uploadID string) (UploadStatus, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return UploadStatus{}, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	u, err := s.loadUpload(ctx, tx, institutionID, uploader, uploadID)
	if err != nil {
		return UploadStatus{}, err
	}
	status := UploadStatus{UploadID: u.ID, Status: u.Status, Kind: u.Kind}
	if u.GradingID == nil {
		return status, nil
	}
	status.GradingID = *u.GradingID
	current, err := s.currentGrading(ctx, tx, *u.GradingID, false)
	if err != nil {
		return UploadStatus{}, storeUnavailable(ctx, err)
	}
	status.CanConfirm = u.Status == "parsed" && u.ExpiresAt.After(s.now()) && transitionProblem(u.Kind, uploader, current) == ""
	status.RequiresCredit = status.CanConfirm && u.Kind == KindInitial && current == nil
	return status, nil
}

// Cancel discards a preview.
func (s *Service) Cancel(ctx context.Context, institutionID, uploader, uploadID string) (map[string]string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	u, err := s.loadUpload(ctx, tx, institutionID, uploader, uploadID)
	if err != nil {
		return nil, err
	}
	if u.Status == "confirmed" {
		return nil, rpc.Fail(rpc.CodeConflict, "This upload is already published")
	}
	if _, err := tx.Exec(ctx, `UPDATE uploads SET status = 'cancelled', parsed = NULL WHERE id = $1`, u.ID); err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	return map[string]string{"upload_id": u.ID, "status": "cancelled"}, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Headers lists every grading's header (for reconciliation).
func (s *Service) Headers(ctx context.Context) ([]messages.GradingHeader, error) {
	rows, err := s.DB.Query(ctx, headerSQL+` ORDER BY g.id`)
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	defer rows.Close()
	headers := []messages.GradingHeader{}
	for rows.Next() {
		h, err := scanHeader(rows)
		if err != nil {
			return nil, storeUnavailable(ctx, err)
		}
		headers = append(headers, h)
	}
	if err := rows.Err(); err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	return headers, nil
}

const headerSQL = `SELECT g.id, g.institution_id, g.course_code, g.course_title, g.period, g.state, g.version,
	g.instructor_id, COALESCE(g.grading_scale, ''), g.question_weights, g.opened_at, g.finalized_at,
	(SELECT count(*) FROM grades WHERE grading_id = g.id) FROM gradings g`

func scanHeader(row pgx.Row) (messages.GradingHeader, error) {
	var h messages.GradingHeader
	var instructor *string
	err := row.Scan(&h.GradingID, &h.InstitutionID, &h.CourseCode, &h.CourseTitle, &h.Period, &h.State, &h.Version,
		&instructor, &h.GradingScale, &h.QuestionWeights, &h.OpenedAt, &h.FinalizedAt, &h.StudentCount)
	if instructor != nil {
		h.InstructorID = *instructor
	}
	if h.QuestionWeights == nil {
		h.QuestionWeights = []float64{}
	}
	return h, err
}

// Snapshot returns a grading's header and grades at its current version.
func (s *Service) Snapshot(ctx context.Context, gradingID string) (messages.GradingSnapshot, error) {
	if _, err := uuid.Parse(gradingID); err != nil {
		return messages.GradingSnapshot{}, rpc.Fail(rpc.CodeNotFound, "The grading was not found")
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	header, err := scanHeader(tx.QueryRow(ctx, headerSQL+` WHERE g.id = $1`, gradingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return messages.GradingSnapshot{}, rpc.Fail(rpc.CodeNotFound, "The grading was not found")
	}
	if err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	rows, err := tx.Query(ctx, `SELECT student_id, COALESCE(student_name, ''), total::float8, question_scores::float8[]
		FROM grades WHERE grading_id = $1 ORDER BY student_id`, gradingID)
	if err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	defer rows.Close()
	snapshot := messages.GradingSnapshot{Type: messages.TypeGradingSnapshot, GradingHeader: header, Grades: []messages.StudentGrade{}}
	for rows.Next() {
		var g messages.StudentGrade
		if err := rows.Scan(&g.StudentID, &g.StudentName, &g.Total, &g.QuestionScores); err != nil {
			return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
		}
		snapshot.Grades = append(snapshot.Grades, g)
	}
	if err := rows.Err(); err != nil {
		return messages.GradingSnapshot{}, storeUnavailable(ctx, err)
	}
	return snapshot, nil
}
