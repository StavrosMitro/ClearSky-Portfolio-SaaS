package accounts

import (
	"strings"
	"testing"

	"user_management_service/internal/model"

	"github.com/google/uuid"
)

func TestParseRosterAcceptsSpreadsheetExports(t *testing.T) {
	for name, text := range map[string]string{
		"comma":             "student_id,email\n03100001,Alice@Uni.Example\n03100002,bob@uni.example\n",
		"semicolon and BOM": "\ufeffStudent_ID;EMAIL;name\r\n03100001;alice@uni.example;Alice\r\n\r\n03100002;bob@uni.example;Bob\r\n",
		"reordered columns": "email,student_id\nalice@uni.example,03100001\nbob@uni.example,03100002",
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := ParseRoster(text)
			if err != nil {
				t.Fatalf("ParseRoster: %v", err)
			}
			if len(rows) != 2 || rows[0].StudentID != "03100001" || rows[0].Email != "alice@uni.example" || rows[1].StudentID != "03100002" {
				t.Fatalf("rows = %+v", rows)
			}
		})
	}
}

func TestParseRosterReportsProblemsByLineWithoutEchoingData(t *testing.T) {
	text := "student_id,email\n" +
		"03100001,alice@uni.example\n" +
		"03100002,not-an-email\n" +
		"bad id!,carol@uni.example\n" +
		"03100001,dave@uni.example\n" +
		"03100005,alice@uni.example\n"
	_, err := ParseRoster(text)
	assertCode(t, err, "INVALID_REQUEST")
	message := err.(*Error).Message
	for _, want := range []string{"line 3: invalid email", "line 4: invalid student ID", "line 5: student ID repeats line 2", "line 6: email repeats line 2"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "not-an-email") || strings.Contains(message, "carol") {
		t.Errorf("message echoes uploaded data: %q", message)
	}
}

func TestParseRosterRejectsMissingHeaderOrRows(t *testing.T) {
	for name, text := range map[string]string{
		"no header":      "03100001,alice@uni.example\n",
		"empty":          "",
		"header only":    "student_id,email\n",
		"too large file": "student_id,email\n" + strings.Repeat("x", RosterMaxBytes),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRoster(text)
			assertCode(t, err, "INVALID_REQUEST")
		})
	}
}

func TestImportRosterUpsertsAndRejectsConflictsAtomically(t *testing.T) {
	env := newTestEnv(t)

	result, err := env.svc.ImportRoster("student_id,email\n03100001,alice@uni.example\n03100002,bob@uni.example\n", "rep-1")
	if err != nil || result != (RosterImportResult{Received: 2, Inserted: 2}) {
		t.Fatalf("first import = %+v, %v", result, err)
	}

	// Unclaimed entries can be corrected by re-uploading.
	result, err = env.svc.ImportRoster("student_id,email\n03100001,alice@uni.example\n03100002,robert@uni.example\n03100003,carol@uni.example\n", "rep-1")
	if err != nil || result != (RosterImportResult{Received: 3, Inserted: 1, Updated: 1, Unchanged: 1}) {
		t.Fatalf("second import = %+v, %v", result, err)
	}

	// Once a student has registered, their entry is locked.
	if err := env.db.Create(&model.User{ID: uuid.NewString(), Username: "robert@uni.example", Role: RoleStudent, StudentID: "03100002"}).Error; err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"claimed entry changes email": "student_id,email\n03100004,dan@uni.example\n03100002,bob2@uni.example\n",
		"email owned by another ID":   "student_id,email\n03100004,dan@uni.example\n03100009,carol@uni.example\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := env.svc.ImportRoster(text, "rep-1")
			assertCode(t, err, "CONFLICT")
		})
	}

	var entries []model.StudentRosterEntry
	if err := env.db.Order("student_id").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range entries {
		got[entry.StudentID] = entry.Email
	}
	want := map[string]string{"03100001": "alice@uni.example", "03100002": "robert@uni.example", "03100003": "carol@uni.example"}
	if len(got) != len(want) {
		t.Fatalf("a rejected file must not change the roster: %v", got)
	}
	for id, email := range want {
		if got[id] != email {
			t.Fatalf("roster = %v, want %v", got, want)
		}
	}
}
