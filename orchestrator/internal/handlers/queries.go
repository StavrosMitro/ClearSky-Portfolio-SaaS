package handlers

import (
	"net/http"

	"orchestrator/internal/api"
	mw "orchestrator/internal/middleware"

	"clearsky/contracts/topology"

	"github.com/gin-gonic/gin"
)

// viewer is the verified caller, used by grades-query for visibility.
func viewer(c *gin.Context, msgType string) map[string]any {
	return map[string]any{"type": msgType, "institution_id": mw.GetInstitutionID(c), "role": mw.GetRole(c),
		"user_id": mw.GetUserID(c), "student_id": mw.GetStudentID(c)}
}

// HandlePersonalGrades lists the student's grades (SRS 2.7).
func HandlePersonalGrades(c *gin.Context, m Messenger) {
	var grades []map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyGradesQuery, viewer(c, "student_grades"), &grades); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, grades)
}

// HandleVisibleGradings lists the gradings the caller may see (SRS 2.6).
func HandleVisibleGradings(c *gin.Context, m Messenger) {
	var gradings []map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyGradesQuery, viewer(c, "visible_gradings"), &gradings); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, gradings)
}

// HandleDistributions returns a grading's precomputed charts.
func HandleDistributions(c *gin.Context, m Messenger) {
	req := viewer(c, "distributions")
	req["grading_id"] = c.Param("id")
	var result map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyGradesQuery, req, &result); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, result)
}
