package ingest

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const characterization = "../../../tests/characterization"

type goldenDoc map[string]any

func loadGolden(t *testing.T, name string) []goldenDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(characterization, "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct{ Docs []goldenDoc }
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return golden.Docs
}

// TestParseMatchesLegacyBehaviour checks every fixture against what the
// legacy Node services extracted (tests/characterization, roadmap 2.2).
// Legacy stored score×weight; the new parser keeps raw scores and weights.
func TestParseMatchesLegacyBehaviour(t *testing.T) {
	for _, name := range []string{"physics-initial", "physics-final", "software-initial"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(characterization, "fixtures", name+".xlsx"))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(parsed.Problems) != 0 {
				t.Fatalf("unexpected problems: %v", parsed.Problems)
			}
			golden := loadGolden(t, name)
			if len(parsed.Rows) != len(golden) {
				t.Fatalf("rows = %d, legacy parsed %d", len(parsed.Rows), len(golden))
			}
			first := golden[0]
			if parsed.Period != first["declarationPeriod"] || !strings.HasSuffix(first["classTitle"].(string), "("+parsed.CourseCode+")") {
				t.Fatalf("course/period = %q/%q, legacy %q/%q", parsed.CourseCode, parsed.Period, first["classTitle"], first["declarationPeriod"])
			}
			for i, row := range parsed.Rows {
				doc := golden[i]
				if row.StudentID != doc["AM"] || row.StudentName != doc["name"] {
					t.Fatalf("row %d identity = %s/%s, legacy %v/%v", i, row.StudentID, row.StudentName, doc["AM"], doc["name"])
				}
				if row.Total == nil || math.Abs(*row.Total-doc["grade"].(float64)) > 1e-9 {
					t.Fatalf("row %d total = %v, legacy %v", i, row.Total, doc["grade"])
				}
				for q := 0; q < 10; q++ {
					legacy := doc["Q"+string(rune('1'+q))]
					if q == 9 {
						legacy = doc["Q10"]
					}
					if q >= len(parsed.Weights) {
						if legacy != nil {
							t.Fatalf("row %d Q%d: legacy has a value beyond the weighted questions", i, q+1)
						}
						continue
					}
					score := row.QuestionScores[q]
					switch {
					case score == nil && legacy != nil, score != nil && legacy == nil:
						t.Fatalf("row %d Q%d blank mismatch: %v vs legacy %v", i, q+1, score, legacy)
					case score != nil && math.Abs(*score*parsed.Weights[q]-legacy.(float64)) > 1e-9:
						t.Fatalf("row %d Q%d weighted = %v, legacy %v", i, q+1, *score*parsed.Weights[q], legacy)
					}
				}
			}
		})
	}
}

func TestParseReportsProblemsByLine(t *testing.T) {
	header := []string{"Αριθμός Μητρώου", "Ονοματεπώνυμο", "Ακαδημαϊκό E-mail", "Περίοδος δήλωσης", "Τμήμα Τάξης", "Κλίμακα βαθμολόγησης", "Βαθμολογία", "", "Q1"}
	row := func(am, course, total, q1 string) []string {
		return []string{am, "Name", "e@x.gr", "2025 ΧΕΙΜ", course, "0-10", total, "", q1}
	}
	parsed := parseRows([][]string{
		{"title"},
		{"", "", "", "", "", "", "", "", "0,5"},
		header,
		row("0311", "ΦΥΣΙΚΗ (3101)", "7,5", "4"),
		row("0312", "ΦΥΣΙΚΗ (3101)", "11", "4"),
		row("0311", "ΦΥΣΙΚΗ (3101)", "5", "4"),
		row("0314", "ΧΗΜΕΙΑ (3102)", "5", "-1"),
		row("", "ΦΥΣΙΚΗ (3101)", "5", "4"),
		{},
	})
	want := []string{
		"Line 5: the grade must be a number from 0 to 10",
		"Line 6: the student ID repeats line 4",
		"Line 7: every row must belong to the same course and exam period",
		"Line 7: question 1 must be a non-negative number",
		"Line 8: the student ID is missing",
	}
	if strings.Join(parsed.Problems, "\n") != strings.Join(want, "\n") {
		t.Fatalf("problems:\n%s\nwant:\n%s", strings.Join(parsed.Problems, "\n"), strings.Join(want, "\n"))
	}
	if parsed.CourseCode != "3101" || parsed.CourseTitle != "ΦΥΣΙΚΗ" || *parsed.Rows[0].Total != 7.5 || parsed.Weights[0] != 0.5 {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestParseRejectsWrongLayout(t *testing.T) {
	if parsed := parseRows([][]string{{"a"}, {"b"}}); len(parsed.Problems) == 0 {
		t.Fatal("a short sheet must be rejected")
	}
	parsed := parseRows([][]string{{}, {}, {"Ονοματεπώνυμο"}, {"x"}})
	if len(parsed.Problems) != 4 || !strings.Contains(parsed.Problems[0], "Αριθμός Μητρώου") {
		t.Fatalf("missing headers: %v", parsed.Problems)
	}
	if _, err := Parse([]byte("not a workbook")); err == nil {
		t.Fatal("garbage must not parse")
	}
}

func TestCourseCodeFallsBackToTitle(t *testing.T) {
	p := &Parsed{}
	p.setCourse("ΜΑΘΗΜΑΤΙΚΑ Ι")
	if p.CourseCode != "ΜΑΘΗΜΑΤΙΚΑ Ι" || p.CourseTitle != "ΜΑΘΗΜΑΤΙΚΑ Ι" {
		t.Fatalf("fallback = %+v", p)
	}
	p.setCourse("ΦΥΣΙΚΗ(3101)")
	if p.CourseCode != "3101" || p.CourseTitle != "ΦΥΣΙΚΗ" {
		t.Fatalf("no-space code = %+v", p)
	}
}
