//go:build e2e

// End-to-end tests against a running stack (docker compose up), through the
// proxy, as real users. Every run uses new course codes, student IDs and
// emails, so it can run repeatedly on the same deployment.
//
//	set -a; . ./.env; set +a
//	cd tools && go test -tags e2e -count=1 -v ./e2e
//
// The failure tests stop and start containers with `docker compose`; set
// E2E_SKIP_FAILURES=1 to run only the journey.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"clearsky/tools/api"
	"clearsky/tools/workbook"
)

const password = "E2E-password-1"

var (
	platform = api.Platform{
		Config: api.Config{BaseURL: env("E2E_URL", "https://localhost"), Insecure: true},
		Mail:   api.Mailpit{URL: env("E2E_MAILPIT", "http://127.0.0.1:8025")},
	}
	runID = func() string {
		raw := make([]byte, 3)
		_, _ = rand.Read(raw)
		return hex.EncodeToString(raw)
	}()
	codeSeq int
)

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	return c
}

// result lets a (value, error) call be checked inline: res(f()).must(t).
type result[T any] struct {
	v   T
	err error
}

func res[T any](v T, err error) result[T] { return result[T]{v, err} }

func (r result[T]) must(t *testing.T) T {
	t.Helper()
	if r.err != nil {
		t.Fatal(r.err)
	}
	return r.v
}

func wantStatus(t *testing.T, err error, statuses ...int) {
	t.Helper()
	for _, status := range statuses {
		if api.Is(err, status) {
			return
		}
	}
	t.Fatalf("error = %v, want HTTP %v", err, statuses)
}

// world is one institution's cast for a test: the secretariat, instructors
// and students with fresh identities.
type world struct {
	rep         *api.Session
	instructors []*api.Session
	students    []*api.Session
	roster      []api.RosterEntry
}

func newWorld(t *testing.T, name string, instructors, students int) *world {
	t.Helper()
	c := ctx(t)
	user, pass := os.Getenv("BOOTSTRAP_ADMIN_USERNAME"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if user == "" || pass == "" {
		t.Skip("BOOTSTRAP_ADMIN_USERNAME/PASSWORD are not set (source .env)")
	}
	w := &world{rep: res(platform.SignIn(c, user, pass)).must(t)}
	res(api.EnsureInstitution(c, w.rep, env("BOOTSTRAP_INSTITUTION", "ClearSky"), "secretariat@uni.example")).must(t)
	for i := range students {
		id := fmt.Sprintf("9%s%s%02d", runID, name, i)
		w.roster = append(w.roster, api.RosterEntry{StudentID: id, Email: fmt.Sprintf("e2e-%s-%s-s%d@uni.example", runID, name, i), Name: "Φοιτητής " + id})
	}
	if err := api.UploadRoster(c, w.rep, w.roster); err != nil {
		t.Fatal(err)
	}
	for i := range instructors {
		email := fmt.Sprintf("e2e-%s-%s-i%d@uni.example", runID, name, i)
		w.instructors = append(w.instructors, res(platform.Instructor(c, w.rep, email, password)).must(t))
	}
	for _, r := range w.roster {
		w.students = append(w.students, res(platform.Student(c, r.StudentID, r.Email, password)).must(t))
	}
	return w
}

// course returns a new course with a unique code.
func course(title string) workbook.Course {
	codeSeq++
	return workbook.Course{Code: fmt.Sprintf("E%s%d", runID, codeSeq), Title: title, Period: "2025-2026 ΧΕΙΜ 2025",
		Weights: []float64{0.5, 1.0, 1.5}}
}

func rows(w *world, totals ...float64) []workbook.Row {
	out := make([]workbook.Row, len(totals))
	for i, total := range totals {
		a, b := 6.0, float64(i%11)
		out[i] = workbook.Row{StudentID: w.roster[i].StudentID, Name: w.roster[i].Name, Email: w.roster[i].Email,
			Total: total, Scores: []*float64{&a, nil, &b}}
	}
	return out
}

func publish(t *testing.T, instructor *api.Session, c workbook.Course, kind string, r []workbook.Row) api.Published {
	t.Helper()
	data := res(workbook.Build(c, r)).must(t)
	preview := res(api.UploadGrades(ctx(t), instructor, kind, c.Code+".xlsx", data)).must(t)
	if !preview.CanConfirm {
		t.Fatalf("%s preview rejected: %v", kind, preview.Problems)
	}
	return res(api.Confirm(ctx(t), instructor, preview.UploadID)).must(t)
}

func credits(t *testing.T, rep *api.Session) int {
	t.Helper()
	var inst api.Institution
	if err := rep.JSON(ctx(t), http.MethodGet, "/institution", nil, &inst); err != nil {
		t.Fatal(err)
	}
	return inst.Credits
}

func personal(t *testing.T, student *api.Session, gradingID string) (api.PersonalGrade, error) {
	var grades []api.PersonalGrade
	if err := student.JSON(ctx(t), http.MethodGet, "/personal/grades", nil, &grades); err != nil {
		return api.PersonalGrade{}, err
	}
	for _, g := range grades {
		if g.GradingID == gradingID {
			return g, nil
		}
	}
	return api.PersonalGrade{}, fmt.Errorf("grading %s not visible yet", gradingID)
}

func myReview(t *testing.T, student *api.Session, gradingID string) (api.Review, error) {
	var list []api.Review
	if err := student.JSON(ctx(t), http.MethodGet, "/reviews/mine", nil, &list); err != nil {
		return api.Review{}, err
	}
	for _, r := range list {
		if r.GradingID == gradingID {
			return r, nil
		}
	}
	return api.Review{}, fmt.Errorf("no request for %s", gradingID)
}

func eventually(t *testing.T, what string, check func() error) {
	t.Helper()
	if err := api.Eventually(ctx(t), 30*time.Second, check); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// TestSRSJourney walks SRS 2.2–2.10 once.
func TestSRSJourney(t *testing.T) {
	c := ctx(t)
	w := newWorld(t, "j", 2, 3)
	owner, other := w.instructors[0], w.instructors[1]
	alice, bob, carol := w.students[0], w.students[1], w.students[2]

	t.Run("accounts", func(t *testing.T) {
		me := res(alice.Me(c)).must(t)
		if me.Role != "student" || me.StudentID != w.roster[0].StudentID {
			t.Fatalf("student session = %+v", me)
		}
		// Only students in the registry may register.
		err := platform.Session("stranger").JSON(c, http.MethodPost, "/user/register",
			map[string]string{"student_id": "9" + runID + "zz", "email": "stranger-" + runID + "@uni.example"}, nil)
		wantStatus(t, err, http.StatusForbidden, http.StatusNotFound)
		wantStatus(t, platform.Session("anon").JSON(c, http.MethodGet, "/personal/grades", nil, nil), http.StatusUnauthorized)
		wantStatus(t, alice.JSON(c, http.MethodGet, "/reviews/inbox", nil, nil), http.StatusForbidden)
		wantStatus(t, owner.JSON(c, http.MethodGet, "/personal/grades", nil, nil), http.StatusForbidden)
		wantStatus(t, owner.JSON(c, http.MethodPatch, "/purchase", map[string]int{"amount": 5}, nil), http.StatusForbidden)
		_, err = platform.SignIn(c, w.roster[0].Email, "wrong-password")
		wantStatus(t, err, http.StatusUnauthorized)
	})

	before := credits(t, w.rep)
	if after := res(api.Purchase(c, w.rep, 2)).must(t); after != before+2 {
		t.Fatalf("credits after purchase = %d, want %d", after, before+2)
	}
	before += 2

	crs := course("ΔΟΚΙΜΑΣΤΙΚΟ ΜΑΘΗΜΑ")
	initialRows := rows(w, 4.5, 7.25, 9)
	var gradingID string
	t.Run("initial grades charge one credit", func(t *testing.T) {
		data := res(workbook.Build(crs, initialRows)).must(t)
		preview := res(api.UploadGrades(c, owner, "initial", "initial.xlsx", data)).must(t)
		if !preview.CanConfirm || !preview.RequiresCredit || preview.GradeCount != 3 || preview.CourseCode != crs.Code {
			t.Fatalf("preview = %+v", preview)
		}
		published := res(api.Confirm(c, owner, preview.UploadID)).must(t)
		if published.State != "open" || !published.Charged || published.StudentCount != 3 {
			t.Fatalf("published = %+v", published)
		}
		gradingID = published.GradingID
		again := res(api.Confirm(c, owner, preview.UploadID)).must(t)
		if again.Charged || again.Version != published.Version {
			t.Fatalf("confirming twice = %+v", again)
		}
		if got := credits(t, w.rep); got != before-1 {
			t.Fatalf("credits = %d, want %d", got, before-1)
		}
		// Another instructor cannot take over the course.
		blocked := res(api.UploadGrades(c, other, "initial", "initial.xlsx", data)).must(t)
		if blocked.CanConfirm {
			t.Fatalf("another instructor may publish: %+v", blocked)
		}
	})
	if gradingID == "" {
		t.FailNow()
	}

	t.Run("students see their grades and the statistics", func(t *testing.T) {
		eventually(t, "personal grade", func() error {
			g, err := personal(t, bob, gradingID)
			if err == nil && (g.Total == nil || *g.Total != 7.25 || len(g.QuestionScores) != 3 || g.QuestionScores[1] != nil) {
				t.Fatalf("bob's grade = %+v", g)
			}
			return err
		})
		var available []api.Grading
		if err := carol.JSON(c, http.MethodGet, "/stats/available", nil, &available); err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(available, func(g api.Grading) bool { return g.GradingID == gradingID }) {
			t.Fatal("grading missing from the statistics list")
		}
		var dist struct {
			Distributions map[string]any `json:"distributions"`
		}
		if err := carol.JSON(c, http.MethodGet, "/stats/gradings/"+gradingID+"/distributions", nil, &dist); err != nil {
			t.Fatal(err)
		}
		if _, ok := dist.Distributions["grade"]; !ok || len(dist.Distributions) != 4 {
			t.Fatalf("distributions = %v", dist.Distributions)
		}
	})

	var aliceRequest api.Review
	t.Run("review requests", func(t *testing.T) {
		eventually(t, "review request", func() error {
			var err error
			aliceRequest, err = api.RequestReview(c, alice, gradingID, "Παρακαλώ ελέγξτε το θέμα 3.")
			return err
		})
		_, err := api.RequestReview(c, alice, gradingID, "Δεύτερο αίτημα")
		wantStatus(t, err, http.StatusConflict)
		res(api.RequestReview(c, bob, gradingID, "Και εγώ.")).must(t)

		var inbox []api.Review
		if err := owner.JSON(c, http.MethodGet, "/reviews/inbox", nil, &inbox); err != nil {
			t.Fatal(err)
		}
		if n := len(slices.DeleteFunc(inbox, func(r api.Review) bool { return r.GradingID != gradingID })); n != 2 {
			t.Fatalf("owner's inbox has %d requests for the grading, want 2", n)
		}
		if err := other.JSON(c, http.MethodGet, "/reviews/inbox", nil, &inbox); err != nil || len(inbox) != 0 {
			t.Fatalf("another instructor sees %d requests (%v)", len(inbox), err)
		}
		_, err = api.Reply(c, other, aliceRequest.ID, "reject", "")
		wantStatus(t, err, http.StatusNotFound, http.StatusForbidden)

		res(api.Reply(c, owner, aliceRequest.ID, "total_accept", "Σωστά, διορθώνεται.")).must(t)
		_, err = api.Reply(c, owner, aliceRequest.ID, "reject", "")
		wantStatus(t, err, http.StatusConflict)
		r := res(myReview(t, alice, gradingID)).must(t)
		if r.Status != "answered" || r.ReplyAction == nil || *r.ReplyAction != "total_accept" {
			t.Fatalf("alice's request = %+v", r)
		}
	})

	t.Run("final grades", func(t *testing.T) {
		finalRows := rows(w, 5.5, 7.25, 9)
		published := publish(t, owner, crs, "final", finalRows)
		if published.State != "final" || published.Charged {
			t.Fatalf("final = %+v", published)
		}
		if got := credits(t, w.rep); got != before-1 {
			t.Fatalf("final grades must not charge: credits = %d", got)
		}
		eventually(t, "final grade", func() error {
			g, err := personal(t, alice, gradingID)
			if err == nil && (g.State != "final" || *g.Total != 5.5 || g.QuestionScores != nil) {
				return fmt.Errorf("alice's grade = %+v", g)
			}
			return err
		})
		eventually(t, "bob's pending request closes", func() error {
			r, err := myReview(t, bob, gradingID)
			if err == nil && r.Status != "closed" {
				return fmt.Errorf("status %s", r.Status)
			}
			return err
		})
		_, err := api.RequestReview(c, carol, gradingID, "Αργά")
		wantStatus(t, err, http.StatusConflict)
		data := res(workbook.Build(crs, finalRows)).must(t)
		for _, kind := range []string{"initial", "final"} {
			if p := res(api.UploadGrades(c, owner, kind, "late.xlsx", data)).must(t); p.CanConfirm {
				t.Fatalf("%s upload accepted after final: %+v", kind, p)
			}
		}
	})
}

// ─── Failure tests (roadmap 2.7) ────────────────────────────────────────────

var (
	chaosOnce  sync.Once
	chaosWorld *world
)

func chaos(t *testing.T) *world {
	if os.Getenv("E2E_SKIP_FAILURES") != "" {
		t.Skip("E2E_SKIP_FAILURES is set")
	}
	chaosOnce.Do(func() {
		w := newWorld(t, "f", 1, 2)
		res(api.Purchase(ctx(t), w.rep, 4)).must(t) // one grading per failure test
		chaosWorld = w
	})
	if chaosWorld == nil {
		t.FailNow()
	}
	return chaosWorld
}

func compose(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = res(filepath.Abs(env("E2E_COMPOSE_DIR", "../.."))).must(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker compose %v: %v\n%s", args, err, out)
	}
}

// stopped stops a container for the duration of the test body.
func stopped(t *testing.T, service string, body func()) {
	t.Helper()
	compose(t, "stop", service)
	defer compose(t, "start", service)
	body()
}

func openGrading(t *testing.T, w *world, totals ...float64) (workbook.Course, string) {
	crs := course("ΑΝΘΕΚΤΙΚΟΤΗΤΑ")
	gradingID := publish(t, w.instructors[0], crs, "initial", rows(w, totals...)).GradingID
	eventually(t, "grade visible", func() error { _, err := personal(t, w.students[0], gradingID); return err })
	return crs, gradingID
}

// With the write side's database down, students still read grades and
// statistics; uploads fail cleanly and work again once it is back.
func TestIngestDatabaseDown(t *testing.T) {
	w := chaos(t)
	crs, gradingID := openGrading(t, w, 6, 8)
	data := res(workbook.Build(crs, rows(w, 6.5, 8))).must(t)
	stopped(t, "grades_ingest_db", func() {
		if _, err := personal(t, w.students[0], gradingID); err != nil {
			t.Fatalf("grades unavailable while grades_ingest_db is down: %v", err)
		}
		if err := w.students[1].JSON(ctx(t), http.MethodGet, "/stats/gradings/"+gradingID+"/distributions", nil, nil); err != nil {
			t.Fatalf("statistics unavailable: %v", err)
		}
		_, err := api.UploadGrades(ctx(t), w.instructors[0], "initial", "x.xlsx", data)
		wantStatus(t, err, http.StatusServiceUnavailable, http.StatusGatewayTimeout)
	})
	eventually(t, "upload after recovery", func() error {
		_, err := api.UploadGrades(ctx(t), w.instructors[0], "initial", "x.xlsx", data)
		return err
	})
}

// With the read side's database down, instructors keep publishing; the
// update is applied once the database is back (the sync message waits).
func TestQueryDatabaseDown(t *testing.T) {
	w := chaos(t)
	crs, gradingID := openGrading(t, w, 5, 5)
	stopped(t, "grades_query_db", func() {
		published := publish(t, w.instructors[0], crs, "initial", rows(w, 7.5, 5))
		if published.Version != 2 {
			t.Fatalf("re-upload = %+v", published)
		}
		_, err := personal(t, w.students[0], gradingID)
		wantStatus(t, err, http.StatusServiceUnavailable, http.StatusGatewayTimeout)
	})
	eventually(t, "update applied after recovery", func() error {
		g, err := personal(t, w.students[0], gradingID)
		if err == nil && *g.Total != 7.5 {
			return fmt.Errorf("total %v", *g.Total)
		}
		return err
	})
}

// A stopped read service misses nothing: the snapshot waits in its queue.
func TestQueryServiceDown(t *testing.T) {
	w := chaos(t)
	crs, gradingID := openGrading(t, w, 3, 4)
	stopped(t, "grades_query", func() {
		publish(t, w.instructors[0], crs, "final", rows(w, 3.5, 4))
	})
	eventually(t, "final applied after restart", func() error {
		g, err := personal(t, w.students[0], gradingID)
		if err == nil && (g.State != "final" || math.Abs(*g.Total-3.5) > 1e-9) {
			return fmt.Errorf("grade %+v", g)
		}
		return err
	})
}

// Without the reviews service, grades still work and requests fail
// cleanly; requests work again after it restarts.
func TestReviewsDown(t *testing.T) {
	w := chaos(t)
	_, gradingID := openGrading(t, w, 6, 2)
	stopped(t, "reviews", func() {
		if _, err := personal(t, w.students[1], gradingID); err != nil {
			t.Fatal(err)
		}
		_, err := api.RequestReview(ctx(t), w.students[1], gradingID, "Ελέγξτε το θέμα 1.")
		wantStatus(t, err, http.StatusServiceUnavailable, http.StatusGatewayTimeout)
	})
	eventually(t, "review request after restart", func() error {
		_, err := api.RequestReview(ctx(t), w.students[1], gradingID, "Ελέγξτε το θέμα 1.")
		if api.Is(err, http.StatusConflict) {
			return nil // the request sent while stopped was delivered later
		}
		return err
	})
}

// TestBackupRestore takes a backup now and restores it into a throw-away
// server inside the backup container (roadmap 3.1).
func TestBackupRestore(t *testing.T) {
	if os.Getenv("E2E_SKIP_FAILURES") != "" {
		t.Skip("needs docker compose")
	}
	compose(t, "exec", "-T", "backup", "sh", "/scripts/backup.sh", "once")
	cmd := exec.Command("docker", "compose", "exec", "-T", "backup", "sh", "/scripts/restore-check.sh")
	cmd.Dir = res(filepath.Abs(env("E2E_COMPOSE_DIR", "../.."))).must(t)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "restore check passed") {
		t.Fatalf("restore check: %v\n%s", err, out)
	}
}

// TestLogsHaveNoPersonalData runs last: the logs of this run must not
// contain its emails, password or any JWT (docs/observability.md).
func TestLogsHaveNoPersonalData(t *testing.T) {
	if os.Getenv("E2E_SKIP_FAILURES") != "" {
		t.Skip("needs docker compose")
	}
	cmd := exec.Command("docker", "compose", "logs", "--no-color", "--since", "30m")
	cmd.Dir = res(filepath.Abs(env("E2E_COMPOSE_DIR", "../.."))).must(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose logs: %v", err)
	}
	logs := string(out)
	for _, secret := range []string{"e2e-" + runID, password, "eyJhbGci", "Φοιτητής 9" + runID} {
		if strings.Contains(logs, secret) {
			t.Errorf("the logs contain %q", secret)
		}
	}
}
