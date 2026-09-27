package handlers

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/internal/api"
	mw "orchestrator/internal/middleware"

	"clearsky/contracts/messages"
	"clearsky/contracts/topology"

	"github.com/gin-gonic/gin"
)

const maxWorkbookBytes = 5 << 20

func uploaderFields(c *gin.Context, msgType string) map[string]any {
	return map[string]any{"type": msgType, "institution_id": mw.GetInstitutionID(c), "uploader_id": mw.GetUserID(c)}
}

// HandleGradesUpload parses an initial or final workbook and returns the
// preview to CONFIRM or CANCEL (SRS 2.5, 2.10).
func HandleGradesUpload(c *gin.Context, m Messenger) {
	kind := c.PostForm("kind")
	if kind != "initial" && kind != "final" {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Choose whether these are initial or final grades", nil)
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "An .xlsx workbook is required", nil)
		return
	}
	if !strings.EqualFold(filepath.Ext(file.Filename), ".xlsx") || file.Size > maxWorkbookBytes {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Upload an .xlsx workbook smaller than 5 MiB", nil)
		return
	}
	src, err := file.Open()
	if err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The uploaded file could not be read", nil)
		return
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, maxWorkbookBytes+1))
	if err != nil || len(data) > maxWorkbookBytes {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Upload an .xlsx workbook smaller than 5 MiB", nil)
		return
	}
	req := uploaderFields(c, "preview")
	req["kind"], req["filename"], req["file_base64"] = kind, filepath.Base(file.Filename), base64.StdEncoding.EncodeToString(data)
	var preview map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyGradesIngest, req, &preview); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, preview)
}

// HandleGradesConfirm publishes an upload. It is an orchestrated saga:
//  1. ask grades-ingest whether this creates a new grading;
//  2. if so, charge one credit (idempotent per grading, so retries and
//     duplicate uploads never charge twice);
//  3. commit in grades-ingest (state machine NULL → open → final);
//  4. forward the snapshot to grades-query and the header to reviews.
//
// A lost step-4 message is repaired by the receivers' reconcile.
func HandleGradesConfirm(c *gin.Context, m Messenger) {
	ctx := c.Request.Context()
	uploadID := c.Param("id")
	statusReq := uploaderFields(c, "upload_status")
	statusReq["upload_id"] = uploadID
	var status struct {
		GradingID      string `json:"grading_id"`
		CanConfirm     bool   `json:"can_confirm"`
		RequiresCredit bool   `json:"requires_credit"`
	}
	if err := callJSON(ctx, m, topology.KeyGradesIngest, statusReq, &status); err != nil {
		serviceError(c, err)
		return
	}
	charged := false
	if status.CanConfirm && status.RequiresCredit {
		var balance struct {
			Applied bool `json:"applied"`
		}
		if err := callJSON(ctx, m, topology.KeyInstitutions, map[string]any{
			"type": "charge", "institution_id": mw.GetInstitutionID(c), "grading_id": status.GradingID,
			"actor_user_id": mw.GetUserID(c),
		}, &balance); err != nil {
			serviceError(c, err)
			return
		}
		charged = balance.Applied
	}
	confirmReq := uploaderFields(c, "confirm")
	confirmReq["upload_id"] = uploadID
	var snapshot messages.GradingSnapshot
	if err := callJSON(ctx, m, topology.KeyGradesIngest, confirmReq, &snapshot); err != nil {
		serviceError(c, err)
		return
	}
	synced := forwardGrading(ctx, m, snapshot)
	api.Success(c, http.StatusOK, gin.H{
		"grading_id": snapshot.GradingID, "course_code": snapshot.CourseCode, "course_title": snapshot.CourseTitle,
		"period": snapshot.Period, "state": snapshot.State, "version": snapshot.Version,
		"student_count": len(snapshot.Grades), "charged": charged, "synchronised": synced,
	})
}

// forwardGrading sends the grading to its readers. Student names stay in
// grades-ingest; reviews gets only the header. Failures are logged: the
// receivers reconcile with grades-ingest periodically.
func forwardGrading(parent context.Context, m Messenger, snapshot messages.GradingSnapshot) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	forQuery := snapshot
	forQuery.Type = messages.TypeGradingSnapshot
	forQuery.Grades = make([]messages.StudentGrade, len(snapshot.Grades))
	for i, g := range snapshot.Grades {
		g.StudentName = ""
		forQuery.Grades[i] = g
	}
	ok := true
	if err := publishJSON(ctx, m, topology.KeyGradesSync, forQuery); err != nil {
		slog.WarnContext(ctx, "forward grading to grades-query failed; reconcile will repair it", "grading_id", snapshot.GradingID, "error", err)
		ok = false
	}
	header := messages.HeaderMessage{Type: messages.TypeGradingHeader, GradingHeader: snapshot.GradingHeader}
	if err := publishJSON(ctx, m, topology.KeyReviewsSync, header); err != nil {
		slog.WarnContext(ctx, "forward grading to reviews failed; reconcile will repair it", "grading_id", snapshot.GradingID, "error", err)
		ok = false
	}
	return ok
}

// HandleGradesCancel discards a preview.
func HandleGradesCancel(c *gin.Context, m Messenger) {
	req := uploaderFields(c, "cancel")
	req["upload_id"] = c.Param("id")
	var result map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyGradesIngest, req, &result); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, result)
}
