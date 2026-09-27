// Package query serves grades and statistics to students, instructors and
// the secretariat (roadmap 3.7). The updater writes read models from
// grades-ingest snapshots; reads never compute anything expensive.
package query

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"strconv"
	"time"

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
	RoleStudent        = "student"
	RoleInstructor     = "instructor"
	RoleRepresentative = "institution_representative"
)

type Service struct{ DB *pgxpool.Pool }

func storeUnavailable(ctx context.Context, err error) error {
	slog.ErrorContext(ctx, "grades read store", "error", err)
	return rpc.Unavailable("Grades are temporarily unavailable")
}

// Apply stores a snapshot if it is newer than what is stored. Older or
// repeated snapshots change nothing, so delivery order and duplicates do
// not matter.
func (s *Service) Apply(ctx context.Context, snap messages.GradingSnapshot) (bool, error) {
	if _, err := uuid.Parse(snap.GradingID); err != nil || snap.Version < 1 {
		return false, rpc.Fail(rpc.CodeInvalidRequest, "A valid grading snapshot is required")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, storeUnavailable(ctx, err)
	}
	defer tx.Rollback(ctx)
	// Serialise updaters of one grading, even for its first version.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, snap.GradingID); err != nil {
		return false, storeUnavailable(ctx, err)
	}
	var stored int
	err = tx.QueryRow(ctx, `SELECT version FROM course_gradings WHERE grading_id = $1`, snap.GradingID).Scan(&stored)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, storeUnavailable(ctx, err)
	}
	if err == nil && stored >= snap.Version {
		return false, nil
	}
	var instructor *string
	if snap.InstructorID != "" {
		instructor = &snap.InstructorID
	}
	weights := snap.QuestionWeights
	if weights == nil {
		weights = []float64{}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO course_gradings (grading_id, institution_id, course_code, course_title, period,
			instructor_id, state, version, question_weights, student_count, initial_published_at, final_published_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
		ON CONFLICT (grading_id) DO UPDATE SET course_title = EXCLUDED.course_title, instructor_id = EXCLUDED.instructor_id,
			state = EXCLUDED.state, version = EXCLUDED.version, question_weights = EXCLUDED.question_weights,
			student_count = EXCLUDED.student_count, final_published_at = EXCLUDED.final_published_at, updated_at = now()`,
		snap.GradingID, snap.InstitutionID, snap.CourseCode, snap.CourseTitle, snap.Period, instructor, snap.State,
		snap.Version, weights, len(snap.Grades), snap.OpenedAt, snap.FinalizedAt); err != nil {
		return false, storeUnavailable(ctx, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM personal_grades WHERE grading_id = $1`, snap.GradingID); err != nil {
		return false, storeUnavailable(ctx, err)
	}
	final := snap.State == messages.StateFinal
	rows := make([][]any, 0, len(snap.Grades))
	for _, g := range snap.Grades {
		var scores []*float64
		if !final { // detailed grades are temporary (SRS REQ015/REQ016)
			scores = g.QuestionScores
			if scores == nil {
				scores = []*float64{}
			}
		}
		rows = append(rows, []any{snap.GradingID, snap.InstitutionID, g.StudentID, g.Total, scores})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"personal_grades"},
		[]string{"grading_id", "institution_id", "student_id", "total", "question_scores"}, pgx.CopyFromRows(rows)); err != nil {
		return false, storeUnavailable(ctx, err)
	}
	histograms, _ := json.Marshal(Distributions(weights, snap.Grades))
	if _, err := tx.Exec(ctx, `INSERT INTO grade_distributions (grading_id, version, histograms, computed_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (grading_id) DO UPDATE SET version = EXCLUDED.version, histograms = EXCLUDED.histograms, computed_at = now()`,
		snap.GradingID, snap.Version, histograms); err != nil {
		return false, storeUnavailable(ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, storeUnavailable(ctx, err)
	}
	return true, nil
}

// Grading is the public header of a course grading.
type Grading struct {
	GradingID          string     `json:"grading_id"`
	CourseCode         string     `json:"course_code"`
	CourseTitle        string     `json:"course_title"`
	Period             string     `json:"period"`
	State              string     `json:"state"`
	Version            int        `json:"version"`
	StudentCount       int        `json:"student_count"`
	QuestionWeights    []float64  `json:"question_weights"`
	InitialPublishedAt time.Time  `json:"initial_published_at"`
	FinalPublishedAt   *time.Time `json:"final_published_at,omitempty"`
}

const gradingColumns = `g.grading_id, g.course_code, g.course_title, g.period, g.state, g.version, g.student_count,
	g.question_weights, g.initial_published_at, g.final_published_at`

func scanGrading(row pgx.Row, extra ...any) (Grading, error) {
	var g Grading
	err := row.Scan(append([]any{&g.GradingID, &g.CourseCode, &g.CourseTitle, &g.Period, &g.State, &g.Version,
		&g.StudentCount, &g.QuestionWeights, &g.InitialPublishedAt, &g.FinalPublishedAt}, extra...)...)
	if g.QuestionWeights == nil {
		g.QuestionWeights = []float64{}
	}
	return g, err
}

// Viewer is the caller as verified by the gateway.
type Viewer struct {
	InstitutionID string `json:"institution_id"`
	Role          string `json:"role"`
	UserID        string `json:"user_id"`
	StudentID     string `json:"student_id"`
}

// visibility returns the SQL condition (on alias g) for gradings v may see
// (SRS 2.6: "discover visible gradings for the logged-in user").
func (v Viewer) visibility() (string, []any, error) {
	if _, err := uuid.Parse(v.InstitutionID); err != nil {
		return "", nil, rpc.Fail(rpc.CodeForbidden, "A valid institution is required")
	}
	switch v.Role {
	case RoleRepresentative:
		return `g.institution_id = $1`, []any{v.InstitutionID}, nil
	case RoleInstructor:
		return `g.institution_id = $1 AND g.instructor_id = $2`, []any{v.InstitutionID, v.UserID}, nil
	case RoleStudent:
		if v.StudentID == "" {
			return "", nil, rpc.Fail(rpc.CodeForbidden, "Your account has no student ID")
		}
		return `g.institution_id = $1 AND EXISTS (SELECT 1 FROM personal_grades p
			WHERE p.grading_id = g.grading_id AND p.student_id = $2)`, []any{v.InstitutionID, v.StudentID}, nil
	default:
		return "", nil, rpc.Fail(rpc.CodeForbidden, "This role cannot view grades")
	}
}

// Visible lists the gradings the viewer may see, newest period first.
func (s *Service) Visible(ctx context.Context, v Viewer) ([]Grading, error) {
	where, args, err := v.visibility()
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+gradingColumns+` FROM course_gradings g WHERE `+where+
		` ORDER BY g.initial_published_at DESC, g.course_title`, args...)
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	defer rows.Close()
	list := []Grading{}
	for rows.Next() {
		g, err := scanGrading(rows)
		if err != nil {
			return nil, storeUnavailable(ctx, err)
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

// DistributionsFor returns the precomputed charts of a visible grading.
func (s *Service) DistributionsFor(ctx context.Context, v Viewer, gradingID string) (map[string]any, error) {
	if _, err := uuid.Parse(gradingID); err != nil {
		return nil, rpc.Fail(rpc.CodeNotFound, "The grading was not found")
	}
	where, args, err := v.visibility()
	if err != nil {
		return nil, err
	}
	var raw []byte
	g, err := scanGrading(s.DB.QueryRow(ctx, `SELECT `+gradingColumns+`, d.histograms FROM course_gradings g
		JOIN grade_distributions d ON d.grading_id = g.grading_id
		WHERE g.grading_id = $`+strconv.Itoa(len(args)+1)+` AND `+where, append(args, gradingID)...), &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpc.Fail(rpc.CodeNotFound, "The grading was not found")
	}
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	var histograms map[string]Histogram
	if err := json.Unmarshal(raw, &histograms); err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	return map[string]any{"grading": g, "distributions": histograms}, nil
}

// StudentGrade is one of the caller's own grades.
type StudentGrade struct {
	Grading
	Total          *float64   `json:"total"`
	QuestionScores []*float64 `json:"question_scores"` // null once final
}

// StudentGrades lists the student's grades (SRS 2.7).
func (s *Service) StudentGrades(ctx context.Context, v Viewer) ([]StudentGrade, error) {
	if v.Role != RoleStudent || v.StudentID == "" {
		return nil, rpc.Fail(rpc.CodeForbidden, "Only students have personal grades")
	}
	rows, err := s.DB.Query(ctx, `SELECT `+gradingColumns+`, p.total, p.question_scores
		FROM personal_grades p JOIN course_gradings g ON g.grading_id = p.grading_id
		WHERE p.institution_id = $1 AND p.student_id = $2 ORDER BY g.initial_published_at DESC, g.course_title`,
		v.InstitutionID, v.StudentID)
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	defer rows.Close()
	list := []StudentGrade{}
	for rows.Next() {
		var sg StudentGrade
		if sg.Grading, err = scanGrading(rows, &sg.Total, &sg.QuestionScores); err != nil {
			return nil, storeUnavailable(ctx, err)
		}
		list = append(list, sg)
	}
	return list, rows.Err()
}

// HasGrade tells whether a student has a grade in a grading, and its state
// (reviews may only be requested on open gradings the student is in).
func (s *Service) HasGrade(ctx context.Context, v Viewer, gradingID string) (map[string]any, error) {
	if _, err := uuid.Parse(gradingID); err != nil {
		return map[string]any{"has_grade": false}, nil
	}
	var state string
	err := s.DB.QueryRow(ctx, `SELECT g.state FROM personal_grades p JOIN course_gradings g ON g.grading_id = p.grading_id
		WHERE p.grading_id = $1 AND p.institution_id = $2 AND p.student_id = $3`, gradingID, v.InstitutionID, v.StudentID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{"has_grade": false}, nil
	}
	if err != nil {
		return nil, storeUnavailable(ctx, err)
	}
	return map[string]any{"has_grade": true, "state": state}, nil
}

// Versions returns the stored version of every grading (for reconcile).
func (s *Service) Versions(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT grading_id::text, version FROM course_gradings`)
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
