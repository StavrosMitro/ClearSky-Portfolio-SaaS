package handlers

import (
	"context"
	"net/http"

	"orchestrator/internal/api"

	"github.com/gin-gonic/gin"
)

type PurchaseRequest struct {
	Name   string `json:"name" binding:"required"`
	Amount int    `json:"amount" binding:"required,gt=0"`
}
type PurchaseResponse struct {
	Message string `json:"message"`
}
type SpendReq struct {
	Name   string `json:"name"`
	Amount int    `json:"amount"`
}
type AvailableReq struct {
	Name string `json:"name" binding:"required"`
}
type AvailableResp struct {
	Credits int `json:"credits"`
}

func HandleCreditsAvail(c *gin.Context, m Messenger) {
	var req AvailableReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "A valid institution name is required", nil)
		return
	}
	var response AvailableResp
	if err := callJSON(c.Request.Context(), m, "credits.avail", req, &response); err != nil {
		messagingError(c, err)
		return
	}
	api.Success(c, http.StatusOK, response)
}
func HandleCreditsSpent(ctx context.Context, m Messenger) error {
	return publishJSON(ctx, m, "credits.spent", SpendReq{Name: "NTUA", Amount: 1})
}
func HandleFinalGradesInc(ctx context.Context, req PurchaseRequest, m Messenger) error {
	return publishJSON(ctx, m, "incr.credits", req)
}
func HandleCreditsPurchased(c *gin.Context, m Messenger) {
	var req PurchaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "A valid institution name and positive amount are required", nil)
		return
	}
	var response PurchaseResponse
	if err := callJSON(c.Request.Context(), m, "credits.purchased", req, &response); err != nil {
		messagingError(c, err)
		return
	}
	if err := HandleFinalGradesInc(c.Request.Context(), req, m); err != nil {
		messagingError(c, err)
		return
	}
	api.Success(c, http.StatusOK, response)
}
