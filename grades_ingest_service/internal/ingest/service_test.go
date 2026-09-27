package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clearsky/contracts/ids"
	"clearsky/contracts/messages"
	"clearsky/contracts/pgtest"
	"clearsky/contracts/rpc"

	"github.com/google/uuid"
)

type env struct {
	svc   *Service
	clock time.Time
	inst  string
	prof  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{clock: time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), inst: ids.Institution("NTUA"), prof: uuid.NewString()}
	e.svc = &Service{DB: pgtest.Pool(t, Migrations()), Now: func() time.Time { return e.clock }}
	return e
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(characterization, "fixtures", name+".xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	rpcErr, ok := rpc.AsError(err)
	if !ok || rpcErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func (e *env) preview(t *testing.T, uploader, kind, name string) Preview {
	t.Helper()
	p, err := e.svc.CreatePreview(context.Background(), e.inst, uploader, kind, name+".xlsx", fixture(t, name))
	if err != nil {
		t.Fatalf("preview %s: %v", name, err)
	}
	return p
}

func (e *env) confirm(t *testing.T, uploader string, p Preview) messages.GradingSnapshot {
	t.Helper()
	snapshot, err := e.svc.Confirm(context.Background(), e.inst, uploader, p.UploadID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return snapshot
}

func TestStateMachineNullOpenFinal(t *testing.T) {
	e := newEnv(t)

	first := e.preview(t, e.prof, KindInitial, "physics-initial")
	if !first.CanConfirm || !first.RequiresCredit || first.CurrentState != "" || first.GradeCount != 30 || first.QuestionCount != 4 {
		t.Fatalf("first preview = %+v", first)
	}
	if first.GradingID != ids.Grading(e.inst, "3101", "2024-2025 ΧΕΙΜ 2024") || first.CourseTitle != "ΦΥΣΙΚΗ" {
		t.Fatalf("grading identity = %s %q", first.GradingID, first.CourseTitle)
	}
	opened := e.confirm(t, e.prof, first)
	if opened.State != messages.StateOpen || opened.Version != 1 || opened.InstructorID != e.prof || len(opened.Grades) != 30 || opened.StudentCount != 30 {
		t.Fatalf("opened = %+v", opened.GradingHeader)
	}
	if again := e.confirm(t, e.prof, first); again.Version != 1 {
		t.Fatalf("confirming twice must be a no-op, got version %d", again.Version)
	}

	// Re-posting initial grades replaces them and does not need a credit.
	redo := e.preview(t, e.prof, KindInitial, "physics-initial")
	if !redo.CanConfirm || redo.RequiresCredit || redo.CurrentState != messages.StateOpen {
		t.Fatalf("re-upload preview = %+v", redo)
	}
	if v := e.confirm(t, e.prof, redo).Version; v != 2 {
		t.Fatalf("re-upload version = %d", v)
	}

	// Another instructor may not touch this grading.
	intruder := uuid.NewString()
	blocked := e.preview(t, intruder, KindFinal, "physics-final")
	if blocked.CanConfirm || !strings.Contains(strings.Join(blocked.Problems, " "), "Another instructor") {
		t.Fatalf("intruder preview = %+v", blocked)
	}
	_, err := e.svc.Confirm(context.Background(), e.inst, intruder, blocked.UploadID)
	assertCode(t, err, rpc.CodeConflict)

	final := e.confirm(t, e.prof, e.preview(t, e.prof, KindFinal, "physics-final"))
	if final.State != messages.StateFinal || final.Version != 3 || final.FinalizedAt == nil {
		t.Fatalf("final = %+v", final.GradingHeader)
	}
	if got := final.Grades; *got[0].Total != 9.2 || *got[1].Total != 4.8 {
		t.Fatalf("final grades not applied: %v %v", *got[0].Total, *got[1].Total)
	}

	for _, kind := range []string{KindInitial, KindFinal} {
		locked := e.preview(t, e.prof, kind, "physics-final")
		if locked.CanConfirm || locked.CurrentState != messages.StateFinal || !strings.Contains(strings.Join(locked.Problems, " "), "final") {
			t.Fatalf("%s after final = %+v", kind, locked)
		}
	}
}

func TestFinalRequiresInitial(t *testing.T) {
	e := newEnv(t)
	p := e.preview(t, e.prof, KindFinal, "software-initial")
	if p.CanConfirm || p.RequiresCredit || !strings.Contains(strings.Join(p.Problems, " "), "initial grades") {
		t.Fatalf("final without initial = %+v", p)
	}
}

func TestSnapshotKeepsRawScoresAndBlanks(t *testing.T) {
	e := newEnv(t)
	snapshot := e.confirm(t, e.prof, e.preview(t, e.prof, KindInitial, "software-initial"))
	parsed, err := Parse(fixture(t, "software-initial"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuestionWeights) != 10 || snapshot.QuestionWeights[5] != 1.2 {
		t.Fatalf("weights = %v", snapshot.QuestionWeights)
	}
	byID := map[string]messages.StudentGrade{}
	for _, g := range snapshot.Grades {
		byID[g.StudentID] = g
	}
	blanks := 0
	for _, row := range parsed.Rows {
		got := byID[row.StudentID]
		if got.StudentName != row.StudentName || *got.Total != *row.Total || len(got.QuestionScores) != 10 {
			t.Fatalf("%s = %+v, parsed %+v", row.StudentID, got, row)
		}
		for q, want := range row.QuestionScores {
			if (want == nil) != (got.QuestionScores[q] == nil) || (want != nil && *want != *got.QuestionScores[q]) {
				t.Fatalf("%s Q%d = %v, want %v", row.StudentID, q+1, got.QuestionScores[q], want)
			}
			if want == nil {
				blanks++
			}
		}
	}
	if blanks != 34 {
		t.Fatalf("blank cells = %d, want the 34 of the fixture", blanks)
	}
	headers, err := e.svc.Headers(context.Background())
	if err != nil || len(headers) != 1 || headers[0].Version != 1 || headers[0].StudentCount != 25 {
		t.Fatalf("headers = %+v, %v", headers, err)
	}
}

func TestPreviewLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	expired := e.preview(t, e.prof, KindInitial, "physics-initial")
	e.clock = e.clock.Add(previewTTL + time.Minute)
	_, err := e.svc.Confirm(ctx, e.inst, e.prof, expired.UploadID)
	assertCode(t, err, rpc.CodeConflict)

	cancelled := e.preview(t, e.prof, KindInitial, "physics-initial")
	if _, err := e.svc.Cancel(ctx, e.inst, e.prof, cancelled.UploadID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Confirm(ctx, e.inst, e.prof, cancelled.UploadID)
	assertCode(t, err, rpc.CodeConflict)

	mine := e.preview(t, e.prof, KindInitial, "physics-initial")
	_, err = e.svc.Confirm(ctx, e.inst, uuid.NewString(), mine.UploadID)
	assertCode(t, err, rpc.CodeNotFound)
	_, err = e.svc.Confirm(ctx, ids.Institution("Other"), e.prof, mine.UploadID)
	assertCode(t, err, rpc.CodeNotFound)
	_, err = e.svc.Snapshot(ctx, uuid.NewString())
	assertCode(t, err, rpc.CodeNotFound)

	_, err = e.svc.CreatePreview(ctx, e.inst, e.prof, KindInitial, "x.xlsx", []byte("not a workbook"))
	assertCode(t, err, rpc.CodeInvalidRequest)
	_, err = e.svc.CreatePreview(ctx, e.inst, e.prof, "draft", "x.xlsx", fixture(t, "physics-initial"))
	assertCode(t, err, rpc.CodeInvalidRequest)
}

func TestUploadStatusTellsWhetherACreditIsNeeded(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.preview(t, e.prof, KindInitial, "physics-initial")
	status, err := e.svc.Status(ctx, e.inst, e.prof, first.UploadID)
	if err != nil || !status.CanConfirm || !status.RequiresCredit || status.GradingID != first.GradingID {
		t.Fatalf("new grading status = %+v, %v", status, err)
	}
	e.confirm(t, e.prof, first)
	if after, _ := e.svc.Status(ctx, e.inst, e.prof, first.UploadID); after.CanConfirm || after.RequiresCredit || after.Status != "confirmed" {
		t.Fatalf("after confirm = %+v", after)
	}
	redo := e.preview(t, e.prof, KindInitial, "physics-initial")
	if status, _ := e.svc.Status(ctx, e.inst, e.prof, redo.UploadID); !status.CanConfirm || status.RequiresCredit {
		t.Fatalf("re-upload needs no credit: %+v", status)
	}
	_, err = e.svc.Status(ctx, e.inst, uuid.NewString(), redo.UploadID)
	assertCode(t, err, rpc.CodeNotFound)
}

func TestPurgeRemovesOnlyUnpublishedExpiredUploads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	published := e.preview(t, e.prof, KindInitial, "physics-initial")
	e.confirm(t, e.prof, published)
	cancelled := e.preview(t, e.prof, KindInitial, "software-initial")
	if _, err := e.svc.Cancel(ctx, e.inst, e.prof, cancelled.UploadID); err != nil {
		t.Fatal(err)
	}
	expired := e.preview(t, e.prof, KindFinal, "physics-final")

	if n, err := e.svc.PurgeUnpublished(ctx); err != nil || n != 0 {
		t.Fatalf("before expiry purged %d (%v)", n, err)
	}
	e.clock = e.clock.Add(previewTTL + time.Minute)
	fresh := e.preview(t, e.prof, KindFinal, "physics-final")
	if n, err := e.svc.PurgeUnpublished(ctx); err != nil || n != 2 {
		t.Fatalf("purged %d (%v), want the cancelled and the expired upload", n, err)
	}
	for _, id := range []string{cancelled.UploadID, expired.UploadID} {
		_, err := e.svc.Status(ctx, e.inst, e.prof, id)
		assertCode(t, err, rpc.CodeNotFound)
	}
	for _, id := range []string{published.UploadID, fresh.UploadID} {
		if _, err := e.svc.Status(ctx, e.inst, e.prof, id); err != nil {
			t.Fatalf("upload %s was purged: %v", id, err)
		}
	}
}
