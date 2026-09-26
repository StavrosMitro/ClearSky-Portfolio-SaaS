package handlers

import (
	"bytes"
	"encoding/base64"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"io"
	"net/http"
	"orchestrator/internal/api"
	"path/filepath"
	"strings"
)

type ExcelUploadResponse struct {
	Message string `json:"message"`
}

func upload(c *gin.Context, m Messenger, key string, final bool, after func([]byte, string) error) {
	file, err := c.FormFile("file")
	if err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "An Excel file is required", nil)
		return
	}
	if !strings.EqualFold(filepath.Ext(file.Filename), ".xlsx") {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Only .xlsx files are allowed", nil)
		return
	}
	src, err := file.Open()
	if err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The uploaded file could not be read", nil)
		return
	}
	defer src.Close()
	data, err := io.ReadAll(src)
	if err != nil {
		api.Internal(c)
		return
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The uploaded file is not a valid Excel workbook", nil)
		return
	}
	if err := workbook.Close(); err != nil {
		api.Internal(c)
		return
	}
	var response ExcelUploadResponse
	reply, err := m.Call(c.Request.Context(), key, []byte(base64.StdEncoding.EncodeToString(data)))
	if err != nil {
		messagingError(c, err)
		return
	}
	if err := decodeRPCReply(reply, &response); err != nil {
		messagingError(c, err)
		return
	}
	if after != nil {
		if err := after(data, file.Filename); err != nil {
			messagingError(c, err)
			return
		}
	}
	if final {
		api.Success(c, http.StatusOK, gin.H{
			"message": "final grades uploaded and credits deducted",
			"details": response,
		})
		return
	}
	api.Success(c, http.StatusOK, response)
}
func UploadExcelInit(c *gin.Context, m Messenger) {
	upload(c, m, "postgrades.init", false, func(data []byte, name string) error {
		if err := ForwardToStatistics(c.Request.Context(), m, data, name); err != nil {
			return err
		}
		return ForwardToView(c.Request.Context(), m, data, name)
	})
}
func UploadExcelFinal(c *gin.Context, m Messenger) {
	upload(c, m, "postgrades.final", true, func(data []byte, name string) error {
		if err := HandleCreditsSpent(c.Request.Context(), m); err != nil {
			return err
		}
		if err := ForwardToStatistics(c.Request.Context(), m, data, name); err != nil {
			return err
		}
		return ForwardToView(c.Request.Context(), m, data, name)
	})
}
