package query

import (
	"fmt"
	"math"

	"clearsky/contracts/messages"
)

// Histogram is one chart: counts per integer score bin.
type Histogram struct {
	Categories []int `json:"categories"`
	Data       []int `json:"data"`
}

// roundHalfAway is MySQL's ROUND on exact values (the legacy statistics).
func roundHalfAway(x float64) int { return int(math.Round(x)) }

// Distributions reproduces the legacy statistics (tests/characterization):
// the total uses bins 0–10; question q uses bins 0 to its highest rounded
// weighted score, where weighted = raw score × weight. Blank values are
// not counted. Keys: "grade", "Q1", "Q2", ...
func Distributions(weights []float64, grades []messages.StudentGrade) map[string]Histogram {
	out := map[string]Histogram{}
	total := Histogram{Categories: bins(10), Data: make([]int, 11)}
	for _, g := range grades {
		if g.Total == nil {
			continue
		}
		bin := roundHalfAway(*g.Total)
		if bin >= 0 && bin <= 10 {
			total.Data[bin]++
		}
	}
	out["grade"] = total

	for q, weight := range weights {
		var values []int
		maxBin := 0
		for _, g := range grades {
			if q >= len(g.QuestionScores) || g.QuestionScores[q] == nil {
				continue
			}
			bin := roundHalfAway(*g.QuestionScores[q] * weight)
			if bin < 0 {
				continue
			}
			values = append(values, bin)
			maxBin = max(maxBin, bin)
		}
		h := Histogram{Categories: bins(maxBin), Data: make([]int, maxBin+1)}
		for _, bin := range values {
			h.Data[bin]++
		}
		out[fmt.Sprintf("Q%d", q+1)] = h
	}
	return out
}

func bins(maxBin int) []int {
	categories := make([]int, maxBin+1)
	for i := range categories {
		categories[i] = i
	}
	return categories
}
