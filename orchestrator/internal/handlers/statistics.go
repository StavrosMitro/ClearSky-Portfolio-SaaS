package handlers

import (
	"context"
	"encoding/base64"
	"net/http"
	"orchestrator/internal/api"
	"orchestrator/internal/middleware"

	"github.com/gin-gonic/gin"
)

type getGradesRequest struct {
	Course            string `json:"course" binding:"required"`
	DeclarationPeriod string `json:"declarationPeriod" binding:"required"`
	ClassTitle        string `json:"classTitle" binding:"required"`
}

func HandleSubmissionLogs(c *gin.Context, m Messenger) {
	req := map[string]interface{}{"role": middleware.GetRole(c), "user_id": middleware.GetUserID(c)}
	if id := middleware.GetStudentID(c); id != "" {
		req["student_id"] = id
	}
	var response interface{}
	if err := callJSON(c.Request.Context(), m, "stats.avail", req, &response); err != nil {
		messagingError(c, err)
		return
	}
	api.Success(c, http.StatusOK, response)
}
func ForwardToStatistics(ctx context.Context, m Messenger, fileData []byte, filename string) error {
	return m.Send(ctx, "postgrades.statistics", []byte(base64.StdEncoding.EncodeToString(fileData)))
}
func HandleGetDistributions(m Messenger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req getGradesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Course, declaration period and class title are required", nil)
			return
		}
		var response interface{}
		if err := callJSON(c.Request.Context(), m, "stats.get", req, &response); err != nil {
			messagingError(c, err)
			return
		}
		api.Success(c, http.StatusOK, response)
	}
}
func GetRole(c *gin.Context) string      { return middleware.GetRole(c) }
func GetStudentID(c *gin.Context) string { return middleware.GetStudentID(c) }
func GetUserID(c *gin.Context) string    { return middleware.GetUserID(c) }
