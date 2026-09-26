package handlers

import (
	"context"
	"encoding/base64"
	"net/http"
	"orchestrator/internal/api"
	"orchestrator/internal/middleware"

	"github.com/gin-gonic/gin"
)

func ForwardToView(ctx context.Context, m Messenger, fileData []byte, filename string) error {
	return m.Send(ctx, "postgrades.view", []byte(base64.StdEncoding.EncodeToString(fileData)))
}
func HandleGetPersonalGrades(c *gin.Context, m Messenger) {
	id := middleware.GetStudentID(c)
	if id == "" {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Student identity is required", nil)
		return
	}
	var response []interface{}
	if err := callJSON(c.Request.Context(), m, "view.avail", map[string]string{"AM": id}, &response); err != nil {
		messagingError(c, err)
		return
	}
	api.Success(c, http.StatusOK, response)
}
