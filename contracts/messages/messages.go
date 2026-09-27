// Package messages defines the versioned payloads the orchestrator forwards
// between services, and the email request consumed by notifications.
package messages

import "time"

// Sync message types (the "type" field of the body).
const (
	TypeGradingSnapshot = "grading.snapshot" // grades-ingest → grades-query (header + grades)
	TypeGradingHeader   = "grading.header"   // grades-ingest → reviews (header only)
	TypeEmail           = "email.send"       // any → notifications
)

// Grading states (SRS 1.1.2). A grading that does not exist is NULL.
const (
	StateOpen  = "open"
	StateFinal = "final"
)

// GradingHeader describes one course grading without the grades.
type GradingHeader struct {
	GradingID       string     `json:"grading_id"`
	InstitutionID   string     `json:"institution_id"`
	CourseCode      string     `json:"course_code"`
	CourseTitle     string     `json:"course_title"`
	Period          string     `json:"period"`
	State           string     `json:"state"`
	Version         int        `json:"version"`
	InstructorID    string     `json:"instructor_id,omitempty"`
	GradingScale    string     `json:"grading_scale,omitempty"`
	QuestionWeights []float64  `json:"question_weights"`
	StudentCount    int        `json:"student_count"`
	OpenedAt        time.Time  `json:"opened_at"`
	FinalizedAt     *time.Time `json:"finalized_at,omitempty"`
}

// StudentGrade is one student's grade. Question scores are raw (before the
// weight); a nil entry is a blank cell.
type StudentGrade struct {
	StudentID      string     `json:"student_id"`
	StudentName    string     `json:"student_name,omitempty"`
	Total          *float64   `json:"total"`
	QuestionScores []*float64 `json:"question_scores"`
}

// GradingSnapshot is the complete state of a grading at Version.
type GradingSnapshot struct {
	Type string `json:"type"`
	GradingHeader
	Grades []StudentGrade `json:"grades"`
}

// HeaderMessage carries only the header (for services that need no grades).
type HeaderMessage struct {
	Type string `json:"type"`
	GradingHeader
}

// EmailRequest asks notifications to deliver one email. DedupeKey makes a
// repeated request harmless.
type EmailRequest struct {
	Type          string `json:"type"`
	DedupeKey     string `json:"dedupe_key"`
	InstitutionID string `json:"institution_id,omitempty"`
	Recipient     string `json:"recipient"`
	Template      string `json:"template"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
}
