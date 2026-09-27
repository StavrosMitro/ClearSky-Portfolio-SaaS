package handlers

import (
	"net/http"

	"orchestrator/internal/api"
	mw "orchestrator/internal/middleware"

	"clearsky/contracts/topology"

	"github.com/gin-gonic/gin"
)

// HandleCreateReview files a review request (SRS 2.8). The student must
// have a grade in an open grading; grades-query confirms that first.
func HandleCreateReview(c *gin.Context, m Messenger) {
	var req struct {
		GradingID string `json:"grading_id" binding:"required"`
		Message   string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Choose a course and write a message", nil)
		return
	}
	check := viewer(c, "has_grade")
	check["grading_id"] = req.GradingID
	var grade struct {
		HasGrade bool   `json:"has_grade"`
		State    string `json:"state"`
	}
	if err := callJSON(c.Request.Context(), m, topology.KeyGradesQuery, check, &grade); err != nil {
		serviceError(c, err)
		return
	}
	if !grade.HasGrade {
		api.Failure(c, http.StatusNotFound, api.CodeNotFound, "You have no grade in this course", nil)
		return
	}
	var created map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyReviews, map[string]any{
		"type": "create", "institution_id": mw.GetInstitutionID(c), "student_id": mw.GetStudentID(c),
		"student_user_id": mw.GetUserID(c), "student_display": mw.GetUsername(c),
		"grading_id": req.GradingID, "message": req.Message,
	}, &created); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusCreated, created)
}

// HandleMyReviews lists the student's review requests and their status.
func HandleMyReviews(c *gin.Context, m Messenger) {
	var list []map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyReviews, map[string]any{
		"type": "student_requests", "institution_id": mw.GetInstitutionID(c), "student_id": mw.GetStudentID(c),
	}, &list); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, list)
}

func instructorFields(c *gin.Context, msgType string) map[string]any {
	return map[string]any{"type": msgType, "institution_id": mw.GetInstitutionID(c), "instructor_id": mw.GetUserID(c)}
}

// HandleReviewInbox lists requests on the instructor's gradings (SRS 2.9).
func HandleReviewInbox(c *gin.Context, m Messenger) {
	var list []map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyReviews, instructorFields(c, "inbox"), &list); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, list)
}

// HandleReviewGet returns one request of the instructor's gradings.
func HandleReviewGet(c *gin.Context, m Messenger) {
	req := instructorFields(c, "get")
	req["request_id"] = c.Param("id")
	var review map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyReviews, req, &review); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, review)
}

// HandleReviewReply answers a request: total_accept, partial_accept or reject.
func HandleReviewReply(c *gin.Context, m Messenger) {
	var body struct {
		Action  string `json:"action" binding:"required"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Choose an action", nil)
		return
	}
	req := instructorFields(c, "reply")
	req["request_id"], req["action"], req["message"] = c.Param("id"), body.Action, body.Message
	var review map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyReviews, req, &review); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, review)
}
