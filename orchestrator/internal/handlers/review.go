package handlers

import (
	"net/http"

	"orchestrator/internal/api"
	"orchestrator/internal/middleware"

	"github.com/gin-gonic/gin"
)

type reviewEnvelope struct {
	Body any `json:"body"`
}

func reviewRPC(c *gin.Context, m Messenger, key string, body any) (map[string]interface{}, bool) {
	var response map[string]interface{}
	if err := callJSON(c.Request.Context(), m, key, reviewEnvelope{Body: body}, &response); err != nil {
		messagingError(c, err)
		return nil, false
	}
	return response, true
}

// HandlePostNewRequest updates both review-service projections. Identity is
// always taken from the verified token, never from client JSON.
func HandlePostNewRequest(c *gin.Context, m Messenger) {
	if !middleware.IsStudent(c) {
		api.Failure(c, http.StatusForbidden, api.CodeForbidden, "Only students can submit review requests", nil)
		return
	}
	studentID := middleware.GetStudentID(c)
	userID := middleware.GetUserID(c)
	if studentID == "" || userID == "" {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Student identity is required for review requests", nil)
		return
	}
	var req struct {
		CourseID       string `json:"course_id" binding:"required"`
		StudentMessage string `json:"student_message" binding:"required"`
		ExamPeriod     string `json:"exam_period" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Course, exam period and message are required", nil)
		return
	}
	body := gin.H{
		"exam_period":     req.ExamPeriod,
		"course_id":       req.CourseID,
		"user_id":         userID,
		"student_id":      studentID,
		"student_message": req.StudentMessage,
	}
	studentResponse, ok := reviewRPC(c, m, "student.postNewRequest", body)
	if !ok {
		return
	}
	if _, ok := reviewRPC(c, m, "instructor.insertStudentRequest", body); !ok {
		return
	}
	api.Success(c, http.StatusOK, studentResponse)
}

func HandleGetRequestStatus(c *gin.Context, m Messenger) {
	if !middleware.IsStudent(c) {
		api.Failure(c, http.StatusForbidden, api.CodeForbidden, "Only students can check request status", nil)
		return
	}
	studentID := middleware.GetStudentID(c)
	userID := middleware.GetUserID(c)
	if studentID == "" || userID == "" {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Student identity is required", nil)
		return
	}
	var req struct {
		CourseID   string `json:"course_id" binding:"required"`
		ExamPeriod string `json:"exam_period" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Course and exam period are required", nil)
		return
	}
	response, ok := reviewRPC(c, m, "student.getRequestStatus", gin.H{
		"exam_period": req.ExamPeriod,
		"course_id":   req.CourseID,
		"user_id":     userID,
		"student_id":  studentID,
	})
	if !ok {
		return
	}
	api.Success(c, http.StatusOK, response)
}

func HandlePostResponse(c *gin.Context, m Messenger) {
	if middleware.GetRole(c) != "instructor" {
		api.Failure(c, http.StatusForbidden, api.CodeForbidden, "Only instructors can reply to review requests", nil)
		return
	}
	username := middleware.GetUsername(c)
	if username == "" {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Instructor identity is required", nil)
		return
	}
	var req struct {
		UserID                 string `json:"user_id" binding:"required"`
		ExamPeriod             string `json:"exam_period" binding:"required"`
		InstructorReplyMessage string `json:"instructor_reply_message" binding:"required"`
		InstructorAction       string `json:"instructor_action" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "User, exam period, reply and action are required", nil)
		return
	}
	body := gin.H{
		"exam_period":              req.ExamPeriod,
		"username":                 username,
		"user_id":                  req.UserID,
		"instructor_reply_message": req.InstructorReplyMessage,
		"instructor_action":        req.InstructorAction,
	}
	studentResponse, ok := reviewRPC(c, m, "student.updateInstructorResponse", body)
	if !ok {
		return
	}
	if _, ok := reviewRPC(c, m, "instructor.postResponse", body); !ok {
		return
	}
	api.Success(c, http.StatusOK, studentResponse)
}

func HandleGetRequestList(c *gin.Context, m Messenger) {
	username := middleware.GetUsername(c)
	if username == "" {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Instructor identity is required", nil)
		return
	}
	response, ok := reviewRPC(c, m, "instructor.getRequestsList", gin.H{"username": username})
	if !ok {
		return
	}
	api.Success(c, http.StatusOK, response)
}

func HandleGetRequestInfo(c *gin.Context, m Messenger) {
	var req struct {
		CourseID   string `json:"course_id" binding:"required"`
		UserID     string `json:"user_id" binding:"required"`
		ExamPeriod string `json:"exam_period" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Course, user and exam period are required", nil)
		return
	}
	response, ok := reviewRPC(c, m, "instructor.getRequestInfo", gin.H{
		"exam_period": req.ExamPeriod,
		"course_id":   req.CourseID,
		"user_id":     req.UserID,
	})
	if !ok {
		return
	}
	api.Success(c, http.StatusOK, response)
}
