// Package workbook writes grade workbooks in the e-sec template the platform
// reads (see tests/characterization and grades_ingest_service/internal/ingest):
//
//	row 1: title, row 2: question weights (from column I),
//	row 3: headers, rows 4+: one student per row.
package workbook

import (
	"bytes"
	"fmt"

	"github.com/xuri/excelize/v2"
)

var headers = []any{"Αριθμός Μητρώου", "Ονοματεπώνυμο", "Ακαδημαϊκό E-mail", "Περίοδος δήλωσης",
	"Τμήμα Τάξης", "Κλίμακα βαθμολόγησης", "Βαθμολογία", ""}

// Row is one student's grade. A nil score is a blank cell.
type Row struct {
	StudentID string
	Name      string
	Email     string
	Total     float64
	Scores    []*float64
}

// Course identifies the grading: the code is written in parentheses after
// the title, as the e-sec export does.
type Course struct {
	Code    string
	Title   string
	Period  string
	Weights []float64
}

// Build returns the .xlsx bytes.
func Build(course Course, rows []Row) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Βαθμολόγιο"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	label := fmt.Sprintf("%s   (%s)", course.Title, course.Code)
	weights := make([]any, 8, 8+len(course.Weights))
	for i := range weights {
		weights[i] = ""
	}
	head := append([]any{}, headers...)
	for i, w := range course.Weights {
		weights = append(weights, w)
		head = append(head, fmt.Sprintf("Q%d", i+1))
	}
	sheetRows := [][]any{{"ΒΑΘΜΟΛΟΓΙΟ", label, course.Period}, weights, head}
	for _, r := range rows {
		row := []any{r.StudentID, r.Name, r.Email, course.Period, label, "0-10", r.Total, ""}
		for _, s := range r.Scores {
			if s == nil {
				row = append(row, "")
			} else {
				row = append(row, *s)
			}
		}
		sheetRows = append(sheetRows, row)
	}
	for i, row := range sheetRows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
