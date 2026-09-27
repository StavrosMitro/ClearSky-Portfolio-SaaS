package query

import (
	"context"
	"testing"
	"time"

	"clearsky/contracts/ids"
	"clearsky/contracts/messages"
	"clearsky/contracts/pgtest"
	"clearsky/contracts/rpc"

	"github.com/google/uuid"
)

func f(v float64) *float64 { return &v }

var (
	ntua  = ids.Institution("NTUA")
	other = ids.Institution("Other")
	prof  = uuid.NewString()
)

func snapshot(version int, state string, grades ...messages.StudentGrade) messages.GradingSnapshot {
	opened := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	h := messages.GradingHeader{
		GradingID: ids.Grading(ntua, "3101", "2025 ΧΕΙΜ"), InstitutionID: ntua, CourseCode: "3101",
		CourseTitle: "ΦΥΣΙΚΗ", Period: "2025 ΧΕΙΜ", State: state, Version: version, InstructorID: prof,
		QuestionWeights: []float64{0.5, 1}, OpenedAt: opened,
	}
	if state == messages.StateFinal {
		final := opened.Add(14 * 24 * time.Hour)
		h.FinalizedAt = &final
	}
	return messages.GradingSnapshot{Type: messages.TypeGradingSnapshot, GradingHeader: h, Grades: grades}
}

var (
	alice = messages.StudentGrade{StudentID: "031001", Total: f(7.2), QuestionScores: []*float64{f(8), f(6)}}
	bob   = messages.StudentGrade{StudentID: "031002", Total: f(3.4), QuestionScores: []*float64{nil, f(3)}}
)

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	rpcErr, ok := rpc.AsError(err)
	if !ok || rpcErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestApplyIsIdempotentAndIgnoresStaleVersions(t *testing.T) {
	s := &Service{DB: pgtest.Pool(t, Migrations())}
	ctx := context.Background()
	student := Viewer{InstitutionID: ntua, Role: RoleStudent, StudentID: "031001"}

	if applied, err := s.Apply(ctx, snapshot(2, messages.StateOpen, alice, bob)); err != nil || !applied {
		t.Fatalf("apply v2: %v %v", applied, err)
	}
	if applied, _ := s.Apply(ctx, snapshot(2, messages.StateOpen, alice)); applied {
		t.Fatal("a repeated version must change nothing")
	}
	if applied, _ := s.Apply(ctx, snapshot(1, messages.StateOpen)); applied {
		t.Fatal("an older version arriving late must change nothing")
	}
	grades, err := s.StudentGrades(ctx, student)
	if err != nil || len(grades) != 1 || *grades[0].Total != 7.2 || len(grades[0].QuestionScores) != 2 || grades[0].StudentCount != 2 {
		t.Fatalf("student grades = %+v, %v", grades, err)
	}

	// Final: detailed per-question grades are deleted (SRS REQ016).
	changed := alice
	changed.Total = f(8.1)
	if applied, err := s.Apply(ctx, snapshot(3, messages.StateFinal, changed, bob)); err != nil || !applied {
		t.Fatalf("apply final: %v %v", applied, err)
	}
	grades, _ = s.StudentGrades(ctx, student)
	if grades[0].State != messages.StateFinal || *grades[0].Total != 8.1 || grades[0].QuestionScores != nil || grades[0].FinalPublishedAt == nil {
		t.Fatalf("final grades = %+v", grades[0])
	}
	stats, err := s.DistributionsFor(ctx, student, grades[0].GradingID)
	if err != nil {
		t.Fatal(err)
	}
	charts := stats["distributions"].(map[string]Histogram)
	if charts["grade"].Data[8] != 1 || charts["grade"].Data[3] != 1 || len(charts) != 3 {
		t.Fatalf("charts after final = %+v", charts)
	}
}

func TestVisibilityPerRole(t *testing.T) {
	s := &Service{DB: pgtest.Pool(t, Migrations())}
	ctx := context.Background()
	if _, err := s.Apply(ctx, snapshot(1, messages.StateOpen, alice, bob)); err != nil {
		t.Fatal(err)
	}
	gradingID := ids.Grading(ntua, "3101", "2025 ΧΕΙΜ")
	cases := []struct {
		name    string
		viewer  Viewer
		visible bool
	}{
		{"representative", Viewer{InstitutionID: ntua, Role: RoleRepresentative}, true},
		{"its instructor", Viewer{InstitutionID: ntua, Role: RoleInstructor, UserID: prof}, true},
		{"another instructor", Viewer{InstitutionID: ntua, Role: RoleInstructor, UserID: uuid.NewString()}, false},
		{"enrolled student", Viewer{InstitutionID: ntua, Role: RoleStudent, StudentID: "031002"}, true},
		{"other student", Viewer{InstitutionID: ntua, Role: RoleStudent, StudentID: "039999"}, false},
		{"other institution", Viewer{InstitutionID: other, Role: RoleRepresentative}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			list, err := s.Visible(ctx, c.viewer)
			if err != nil {
				t.Fatal(err)
			}
			if (len(list) == 1) != c.visible {
				t.Fatalf("visible = %d gradings", len(list))
			}
			_, err = s.DistributionsFor(ctx, c.viewer, gradingID)
			if c.visible && err != nil {
				t.Fatalf("distributions: %v", err)
			}
			if !c.visible {
				assertCode(t, err, rpc.CodeNotFound)
			}
		})
	}
	_, err := s.StudentGrades(ctx, Viewer{InstitutionID: ntua, Role: RoleInstructor, UserID: prof})
	assertCode(t, err, rpc.CodeForbidden)

	has, err := s.HasGrade(ctx, Viewer{InstitutionID: ntua, StudentID: "031001"}, gradingID)
	if err != nil || has["has_grade"] != true || has["state"] != messages.StateOpen {
		t.Fatalf("has grade = %v, %v", has, err)
	}
	if has, _ := s.HasGrade(ctx, Viewer{InstitutionID: ntua, StudentID: "039999"}, gradingID); has["has_grade"] != false {
		t.Fatal("a student without a grade has no grade")
	}
}

type fakeSource struct {
	snapshots map[string]messages.GradingSnapshot
	fetched   int
}

func (f *fakeSource) Headers(context.Context) ([]messages.GradingHeader, error) {
	var headers []messages.GradingHeader
	for _, s := range f.snapshots {
		headers = append(headers, s.GradingHeader)
	}
	return headers, nil
}

func (f *fakeSource) Snapshot(_ context.Context, id string) (messages.GradingSnapshot, error) {
	f.fetched++
	return f.snapshots[id], nil
}

func TestReconcileRepairsMissedUpdates(t *testing.T) {
	s := &Service{DB: pgtest.Pool(t, Migrations())}
	ctx := context.Background()
	current := snapshot(3, messages.StateOpen, alice, bob)
	source := &fakeSource{snapshots: map[string]messages.GradingSnapshot{current.GradingID: current}}

	if _, err := s.Apply(ctx, snapshot(1, messages.StateOpen, alice)); err != nil {
		t.Fatal(err)
	}
	applied, err := s.Reconcile(ctx, source)
	if err != nil || applied != 1 {
		t.Fatalf("reconcile = %d, %v", applied, err)
	}
	versions, _ := s.Versions(ctx)
	if versions[current.GradingID] != 3 {
		t.Fatalf("version after reconcile = %d", versions[current.GradingID])
	}
	fetched := source.fetched
	if applied, _ := s.Reconcile(ctx, source); applied != 0 || source.fetched != fetched {
		t.Fatal("an up-to-date store must not fetch anything")
	}
}
