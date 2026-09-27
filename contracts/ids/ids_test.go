package ids

import "testing"

func TestGradingIsStableAndNormalisesWhitespace(t *testing.T) {
	a := Grading("inst-1", "3205", "2024-2025  ΧΕΙΜ 2024")
	b := Grading("inst-1", "3205", " 2024-2025 ΧΕΙΜ 2024 ")
	if a != b {
		t.Fatal("whitespace in the period must not change the ID")
	}
	if a == Grading("inst-2", "3205", "2024-2025 ΧΕΙΜ 2024") || a == Grading("inst-1", "3206", "2024-2025 ΧΕΙΜ 2024") {
		t.Fatal("different institutions or courses must get different IDs")
	}
	if Institution("NTUA") != Institution(" ntua ") {
		t.Fatal("institution IDs ignore case and surrounding space")
	}
}
