package ingest

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"clearsky/contracts/rpc"
)

// request carries every operation's fields. The gateway sets
// institution_id and uploader_id from the caller's verified JWT.
type request struct {
	InstitutionID string `json:"institution_id"`
	UploaderID    string `json:"uploader_id"`
	Kind          string `json:"kind"`
	Filename      string `json:"filename"`
	File          string `json:"file_base64"`
	UploadID      string `json:"upload_id"`
	GradingID     string `json:"grading_id"`
}

// Handle dispatches grades.ingest.request messages.
func (s *Service) Handle(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
	var req request
	if err := rpc.Bind(body, &req); err != nil {
		return nil, err
	}
	switch msgType {
	case "preview":
		file, err := base64.StdEncoding.DecodeString(req.File)
		if err != nil {
			return nil, rpc.Fail(rpc.CodeInvalidRequest, "The workbook is not correctly encoded")
		}
		return s.CreatePreview(ctx, req.InstitutionID, req.UploaderID, req.Kind, req.Filename, file)
	case "upload_status":
		return s.Status(ctx, req.InstitutionID, req.UploaderID, req.UploadID)
	case "confirm":
		return s.Confirm(ctx, req.InstitutionID, req.UploaderID, req.UploadID)
	case "cancel":
		return s.Cancel(ctx, req.InstitutionID, req.UploaderID, req.UploadID)
	case "headers":
		return s.Headers(ctx)
	case "snapshot":
		return s.Snapshot(ctx, req.GradingID)
	default:
		return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown grades operation")
	}
}
