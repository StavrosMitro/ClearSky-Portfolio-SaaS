package query

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"clearsky/contracts/messages"
)

const characterization = "../../../tests/characterization"

// legacyGrades turns the legacy parse (weighted Q values) back into raw
// scores with the weights inferred from the fixture, so the test feeds the
// new code exactly what grades-ingest would send.
func loadGolden(t *testing.T, name string) (map[string]Histogram, []float64, []messages.StudentGrade) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(characterization, "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Docs       []map[string]any
		Histograms map[string]Histogram
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	weights := fixtureWeights[name]
	var grades []messages.StudentGrade
	for _, doc := range golden.Docs {
		g := messages.StudentGrade{StudentID: doc["AM"].(string)}
		if total, ok := doc["grade"].(float64); ok {
			g.Total = &total
		}
		for q, weight := range weights {
			if weighted, ok := doc["Q"+strconv.Itoa(q+1)].(float64); ok {
				score := weighted / weight
				g.QuestionScores = append(g.QuestionScores, &score)
			} else {
				g.QuestionScores = append(g.QuestionScores, nil)
			}
		}
		grades = append(grades, g)
	}
	return golden.Histograms, weights, grades
}

var fixtureWeights = map[string][]float64{
	"physics-initial":  {0.4, 0.6, 1.0, 0.8},
	"physics-final":    {0.4, 0.6, 1.0, 0.8},
	"software-initial": {0.2, 0.4, 0.6, 0.8, 1.0, 1.2, 0.2, 0.4, 0.6, 0.8},
}

// TestDistributionsMatchLegacyStatistics compares with the histograms the
// legacy stats_service served for the same workbooks (roadmap 2.2).
func TestDistributionsMatchLegacyStatistics(t *testing.T) {
	for name := range fixtureWeights {
		t.Run(name, func(t *testing.T) {
			legacy, weights, grades := loadGolden(t, name)
			got := Distributions(weights, grades)
			if !reflect.DeepEqual(got["grade"], legacy["grade"]) {
				t.Fatalf("grade = %+v, legacy %+v", got["grade"], legacy["grade"])
			}
			for q := range weights {
				key := "Q" + strconv.Itoa(q+1)
				if !reflect.DeepEqual(got[key], legacy[key]) {
					t.Fatalf("%s = %+v, legacy %+v", key, got[key], legacy[key])
				}
			}
			if len(got) != 1+len(weights) {
				t.Fatalf("only real questions get a chart, got %d charts", len(got))
			}
		})
	}
}
