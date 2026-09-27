package reviews

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

var (
	ntua    = ids.Institution("NTUA")
	prof    = uuid.NewString()
	grading = ids.Grading(ntua, "3101", "2025 ΧΕΙΜ")
)

func header(version int, state string) messages.GradingHeader {
	return messages.GradingHeader{GradingID: grading, InstitutionID: ntua, CourseCode: "3101", CourseTitle: "ΦΥΣΙΚΗ",
		Period: "2025 ΧΕΙΜ", State: state, Version: version, InstructorID: prof, OpenedAt: time.Now()}
}

func student(am string) Student {
	return Student{InstitutionID: ntua, StudentID: am, UserID: uuid.NewString(), Display: "student " + am}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	rpcErr, ok := rpc.AsError(err)
	if !ok || rpcErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func newService(t *testing.T) *Service {
	t.Helper()
	s := &Service{DB: pgtest.Pool(t, Migrations())}
	if _, err := s.ApplyHeader(context.Background(), header(1, messages.StateOpen)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRequestAndReplyFlow(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	alice := student("031001")

	req, err := s.Create(ctx, alice, grading, "  Please check question 2.  ")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != "pending" || req.Message != "Please check question 2." || req.CourseTitle != "ΦΥΣΙΚΗ" || req.GradingState != "open" {
		t.Fatalf("created = %+v", req)
	}
	_, err = s.Create(ctx, alice, grading, "again")
	assertCode(t, err, rpc.CodeConflict)

	inbox, err := s.Inbox(ctx, ntua, prof)
	if err != nil || len(inbox) != 1 || inbox[0].StudentDisplay != "student 031001" {
		t.Fatalf("inbox = %+v, %v", inbox, err)
	}
	if others, _ := s.Inbox(ctx, ntua, uuid.NewString()); len(others) != 0 {
		t.Fatal("another instructor must not see the request")
	}
	_, err = s.Reply(ctx, ntua, uuid.NewString(), req.ID, "reject", "no")
	assertCode(t, err, rpc.CodeNotFound)
	_, err = s.Reply(ctx, ntua, prof, req.ID, "maybe", "")
	assertCode(t, err, rpc.CodeInvalidRequest)

	answered, err := s.Reply(ctx, ntua, prof, req.ID, "partial_accept", "Question 2 gets +1.")
	if err != nil || answered.Status != "answered" || *answered.ReplyAction != "partial_accept" || answered.RepliedAt == nil {
		t.Fatalf("answered = %+v, %v", answered, err)
	}
	_, err = s.Reply(ctx, ntua, prof, req.ID, "reject", "changed my mind")
	assertCode(t, err, rpc.CodeConflict)

	mine, err := s.StudentRequests(ctx, alice)
	if err != nil || len(mine) != 1 || *mine[0].ReplyMessage != "Question 2 gets +1." {
		t.Fatalf("student view = %+v, %v", mine, err)
	}
}

func TestFinalClosesPendingRequests(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	pending, err := s.Create(ctx, student("031001"), grading, "please")
	if err != nil {
		t.Fatal(err)
	}
	answered, _ := s.Create(ctx, student("031002"), grading, "please")
	if _, err := s.Reply(ctx, ntua, prof, answered.ID, "reject", "no"); err != nil {
		t.Fatal(err)
	}

	if applied, err := s.ApplyHeader(ctx, header(3, messages.StateFinal)); err != nil || !applied {
		t.Fatalf("final header: %v %v", applied, err)
	}
	if applied, _ := s.ApplyHeader(ctx, header(2, messages.StateOpen)); applied {
		t.Fatal("a stale header must not reopen the grading")
	}
	closed, _ := s.ForInstructor(ctx, ntua, prof, pending.ID)
	kept, _ := s.ForInstructor(ctx, ntua, prof, answered.ID)
	if closed.Status != "closed" || kept.Status != "answered" || closed.GradingState != "final" {
		t.Fatalf("after final: pending→%s answered→%s", closed.Status, kept.Status)
	}
	_, err = s.Create(ctx, student("031003"), grading, "too late")
	assertCode(t, err, rpc.CodeConflict)
	_, err = s.Reply(ctx, ntua, prof, pending.ID, "reject", "")
	assertCode(t, err, rpc.CodeConflict)
}

func TestRequestsStayInsideTheInstitution(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	outsider := Student{InstitutionID: ids.Institution("Other"), StudentID: "031001", UserID: uuid.NewString(), Display: "x"}
	_, err := s.Create(ctx, outsider, grading, "hello")
	assertCode(t, err, rpc.CodeNotFound)
	_, err = s.Create(ctx, student("031001"), uuid.NewString(), "hello")
	assertCode(t, err, rpc.CodeNotFound)
	_, err = s.Create(ctx, student("031001"), grading, "   ")
	assertCode(t, err, rpc.CodeInvalidRequest)
}

type headers []messages.GradingHeader

func (h headers) Headers(context.Context) ([]messages.GradingHeader, error) { return h, nil }

func TestReconcileAppliesNewerHeaders(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	applied, err := s.Reconcile(ctx, headers{header(1, messages.StateOpen), header(4, messages.StateFinal)})
	if err != nil || applied != 1 {
		t.Fatalf("reconcile = %d, %v", applied, err)
	}
	if v, _ := s.Versions(ctx); v[grading] != 4 {
		t.Fatalf("version = %d", v[grading])
	}
}
