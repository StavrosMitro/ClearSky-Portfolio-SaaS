package ingest

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// The e-sec workbook template (SRS 2.5): row 1 holds the question weights
// (from column 8), row 2 the headers, rows 3+ one student each.
const (
	weightsRow     = 1
	headerRow      = 2
	firstDataRow   = 3
	firstQuestion  = 8
	maxProblems    = 20
	maxGradeValue  = 10
	headerAM       = "Αριθμός Μητρώου"
	headerName     = "Ονοματεπώνυμο"
	headerPeriod   = "Περίοδος δήλωσης"
	headerCourse   = "Τμήμα Τάξης"
	headerScale    = "Κλίμακα βαθμολόγησης"
	headerTotal    = "Βαθμολογία"
	maxStudentRows = 5000
)

var courseCodePattern = regexp.MustCompile(`\(\s*([\p{L}\p{N}.\-/]+)\s*\)\s*$`)

// Row is one student's grades; question scores are raw (before weights).
type Row struct {
	StudentID      string     `json:"student_id"`
	StudentName    string     `json:"student_name,omitempty"`
	Total          *float64   `json:"total"`
	QuestionScores []*float64 `json:"question_scores"`
}

// Parsed is a workbook after structure and metadata checks (SRS 2.5 activity:
// "structure ok", "metadata ok"). Problems are safe to show: they cite
// lines, never cell contents.
type Parsed struct {
	CourseCode   string    `json:"course_code"`
	CourseTitle  string    `json:"course_title"`
	Period       string    `json:"period"`
	GradingScale string    `json:"grading_scale"`
	Weights      []float64 `json:"weights"`
	Rows         []Row     `json:"rows"`
	Problems     []string  `json:"problems"`
	label        string    // the first row's course label, as written
}

func (p *Parsed) problem(format string, args ...any) {
	if len(p.Problems) < maxProblems {
		p.Problems = append(p.Problems, fmt.Sprintf(format, args...))
	}
}

// Parse reads the first sheet of an .xlsx workbook.
func Parse(data []byte) (*Parsed, error) {
	book, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a valid .xlsx workbook")
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("the workbook has no sheets")
	}
	rows, err := book.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("the first sheet cannot be read")
	}
	return parseRows(rows), nil
}

func parseRows(rows [][]string) *Parsed {
	p := &Parsed{}
	if len(rows) <= firstDataRow {
		p.problem("The workbook must have the e-sec layout: weights on row 2, headers on row 3 and at least one student")
		return p
	}
	columns := map[string]int{}
	for i, title := range rows[headerRow] {
		columns[strings.TrimSpace(title)] = i
	}
	for _, required := range []string{headerAM, headerPeriod, headerCourse, headerTotal} {
		if _, ok := columns[required]; !ok {
			p.problem("Missing column %q on the header row (line %d)", required, headerRow+1)
		}
	}
	if len(p.Problems) > 0 {
		return p
	}
	// Questions are the consecutive columns from 8 that have a numeric weight.
	for col := firstQuestion; col < len(rows[weightsRow]); col++ {
		weight, ok := number(cell(rows[weightsRow], col))
		if !ok {
			break
		}
		if weight < 0 {
			p.problem("Question %d has a negative weight (line %d)", col-firstQuestion+1, weightsRow+1)
		}
		p.Weights = append(p.Weights, weight)
	}

	seen := map[string]int{}
	for index := firstDataRow; index < len(rows); index++ {
		row, line := rows[index], index+1
		if blank(row) {
			continue
		}
		if len(p.Rows) >= maxStudentRows {
			p.problem("The workbook has more than %d students", maxStudentRows)
			break
		}
		course := collapse(cell(row, columns[headerCourse]))
		period := collapse(cell(row, columns[headerPeriod]))
		switch {
		case course == "" || period == "":
			p.problem("Line %d: the course and the exam period are required", line)
		case p.label == "" && p.Period == "":
			p.label = course
			p.setCourse(course)
			p.Period = period
			if i, ok := columns[headerScale]; ok {
				p.GradingScale = collapse(cell(row, i))
			}
		case course != p.label || period != p.Period:
			p.problem("Line %d: every row must belong to the same course and exam period", line)
		}

		studentID := strings.TrimSpace(cell(row, columns[headerAM]))
		if studentID == "" {
			p.problem("Line %d: the student ID is missing", line)
			continue
		}
		if first, dup := seen[studentID]; dup {
			p.problem("Line %d: the student ID repeats line %d", line, first)
			continue
		}
		seen[studentID] = line

		r := Row{StudentID: studentID}
		if i, ok := columns[headerName]; ok {
			r.StudentName = collapse(cell(row, i))
		}
		if raw := strings.TrimSpace(cell(row, columns[headerTotal])); raw != "" {
			total, ok := number(raw)
			if !ok || total < 0 || total > maxGradeValue {
				p.problem("Line %d: the grade must be a number from 0 to 10", line)
			} else {
				r.Total = &total
			}
		}
		for q := range p.Weights {
			raw := strings.TrimSpace(cell(row, firstQuestion+q))
			if raw == "" {
				r.QuestionScores = append(r.QuestionScores, nil)
				continue
			}
			score, ok := number(raw)
			if !ok || score < 0 {
				p.problem("Line %d: question %d must be a non-negative number", line, q+1)
				score = 0
			}
			r.QuestionScores = append(r.QuestionScores, &score)
		}
		p.Rows = append(p.Rows, r)
	}
	if len(p.Rows) == 0 && len(p.Problems) == 0 {
		p.problem("The workbook contains no students")
	}
	return p
}

// setCourse splits "ΤΕΧΝΟΛΟΓΙΑ ΛΟΓΙΣΜΙΚΟΥ (3205)" into title and code; a
// title without a code in parentheses uses the whole title as its code.
func (p *Parsed) setCourse(label string) {
	if m := courseCodePattern.FindStringSubmatchIndex(label); m != nil {
		p.CourseCode = label[m[2]:m[3]]
		p.CourseTitle = strings.TrimSpace(label[:m[0]])
		return
	}
	p.CourseCode, p.CourseTitle = label, label
}

func cell(row []string, index int) string {
	if index < len(row) {
		return row[index]
	}
	return ""
}

func blank(row []string) bool {
	for _, value := range row {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// number accepts "7.5" and the Greek-locale "7,5".
func number(raw string) (float64, bool) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}
