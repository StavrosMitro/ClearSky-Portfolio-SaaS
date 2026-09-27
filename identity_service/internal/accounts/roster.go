package accounts

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"identity_service/internal/model"

	"gorm.io/gorm"
)

const (
	RosterMaxBytes = 2 << 20
	RosterMaxRows  = 20000
	maxReported    = 10
)

type RosterRow struct {
	Line      int
	StudentID string
	Email     string
	FullName  string
}

type RosterImportResult struct {
	Received  int `json:"received"`
	Inserted  int `json:"inserted"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

// ParseRoster reads a CSV with a header row containing student_id and email
// (any order, extra columns ignored). Comma and semicolon separators are
// accepted because spreadsheet exports differ by locale.
func ParseRoster(text string) ([]RosterRow, error) {
	if len(text) > RosterMaxBytes {
		return nil, fail("INVALID_REQUEST", "The roster file must be smaller than 2 MiB")
	}
	text = strings.TrimPrefix(text, "\ufeff")
	firstLine := text
	if end := strings.IndexAny(text, "\r\n"); end >= 0 {
		firstLine = text[:end]
	}
	reader := csv.NewReader(strings.NewReader(text))
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		reader.Comma = ';'
	}
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, fail("INVALID_REQUEST", "The roster must start with the header row: student_id,email")
	}
	idColumn, emailColumn, nameColumn := -1, -1, -1
	for i, name := range header {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "student_id":
			idColumn = i
		case "email":
			emailColumn = i
		case "full_name", "name":
			nameColumn = i
		}
	}
	if idColumn < 0 || emailColumn < 0 {
		return nil, fail("INVALID_REQUEST", "The header row must contain the columns student_id and email")
	}

	var rows []RosterRow
	var problems []string
	seenID, seenEmail := map[string]int{}, map[string]int{}
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				problems = append(problems, fmt.Sprintf("line %d: malformed CSV", parseErr.Line))
			} else {
				problems = append(problems, "malformed CSV")
			}
			break
		}
		line, _ := reader.FieldPos(0)
		if blankRecord(record) {
			continue
		}
		if len(rows) >= RosterMaxRows {
			problems = append(problems, fmt.Sprintf("the roster has more than %d rows", RosterMaxRows))
			break
		}
		studentID, okID := NormalizeStudentID(field(record, idColumn))
		email, okEmail := NormalizeEmail(field(record, emailColumn))
		switch {
		case !okID:
			problems = append(problems, fmt.Sprintf("line %d: invalid student ID", line))
		case !okEmail:
			problems = append(problems, fmt.Sprintf("line %d: invalid email", line))
		case seenID[studentID] != 0:
			problems = append(problems, fmt.Sprintf("line %d: student ID repeats line %d", line, seenID[studentID]))
		case seenEmail[email] != 0:
			problems = append(problems, fmt.Sprintf("line %d: email repeats line %d", line, seenEmail[email]))
		default:
			seenID[studentID], seenEmail[email] = line, line
			fullName := ""
			if nameColumn >= 0 {
				fullName = strings.Join(strings.Fields(field(record, nameColumn)), " ")
			}
			rows = append(rows, RosterRow{Line: line, StudentID: studentID, Email: email, FullName: fullName})
		}
	}
	if len(problems) > 0 {
		return nil, fail("INVALID_REQUEST", summarize("The roster was not imported", problems))
	}
	if len(rows) == 0 {
		return nil, fail("INVALID_REQUEST", "The roster contains no students")
	}
	return rows, nil
}

// ImportRoster adds new entries and corrects the email of entries nobody has
// registered with yet. Any conflict rejects the whole file.
func (s *Service) ImportRoster(text, uploadedBy, institutionID string) (RosterImportResult, error) {
	rows, err := ParseRoster(text)
	if err != nil {
		return RosterImportResult{}, err
	}
	result := RosterImportResult{Received: len(rows)}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		var existing []model.StudentRosterEntry
		if err := tx.Where("institution_id = ?", institutionID).Find(&existing).Error; err != nil {
			return storeUnavailable(err)
		}
		var registered []string
		if err := tx.Model(&model.User{}).Where("institution_id = ? AND student_id <> ''", institutionID).Pluck("student_id", &registered).Error; err != nil {
			return storeUnavailable(err)
		}
		byID := make(map[string]model.StudentRosterEntry, len(existing))
		emailOwner := make(map[string]string, len(existing))
		for _, entry := range existing {
			byID[entry.StudentID] = entry
			emailOwner[entry.Email] = entry.StudentID
		}
		claimed := make(map[string]bool, len(registered))
		for _, id := range registered {
			claimed[id] = true
		}

		now := s.now()
		var inserts []model.StudentRosterEntry
		var conflicts []string
		for _, row := range rows {
			current, known := byID[row.StudentID]
			owner, emailTaken := emailOwner[row.Email]
			switch {
			case known && current.Email == row.Email:
				result.Unchanged++
			case emailTaken && owner != row.StudentID:
				conflicts = append(conflicts, fmt.Sprintf("line %d: email already belongs to another student ID", row.Line))
			case known && claimed[row.StudentID]:
				conflicts = append(conflicts, fmt.Sprintf("line %d: student ID already has an account with a different email", row.Line))
			case known:
				if err := tx.Model(&model.StudentRosterEntry{}).Where("institution_id = ? AND student_id = ?", institutionID, row.StudentID).
					Updates(map[string]any{"email": row.Email, "full_name": row.FullName, "uploaded_by": uploadedBy, "updated_at": now}).Error; err != nil {
					return storeUnavailable(err)
				}
				delete(emailOwner, current.Email)
				emailOwner[row.Email] = row.StudentID
				result.Updated++
			default:
				inserts = append(inserts, model.StudentRosterEntry{InstitutionID: institutionID, StudentID: row.StudentID, Email: row.Email,
					FullName: row.FullName, UploadedBy: uploadedBy, CreatedAt: now, UpdatedAt: now})
				emailOwner[row.Email] = row.StudentID
				result.Inserted++
			}
		}
		if len(conflicts) > 0 {
			return fail("CONFLICT", summarize("The roster was not imported", conflicts))
		}
		if len(inserts) > 0 {
			if err := tx.CreateInBatches(inserts, 500).Error; err != nil {
				return storeUnavailable(err)
			}
		}
		return nil
	})
	if err != nil {
		return RosterImportResult{}, err
	}
	return result, nil
}

func field(record []string, index int) string {
	if index < len(record) {
		return record[index]
	}
	return ""
}

func blankRecord(record []string) bool {
	for _, value := range record {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func summarize(prefix string, problems []string) string {
	shown := problems
	if len(shown) > maxReported {
		shown = shown[:maxReported]
	}
	message := prefix + ": " + strings.Join(shown, "; ")
	if extra := len(problems) - len(shown); extra > 0 {
		message += fmt.Sprintf(" (and %d more)", extra)
	}
	return message
}
