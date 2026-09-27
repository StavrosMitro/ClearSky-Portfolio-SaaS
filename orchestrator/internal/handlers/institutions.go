package handlers

import (
	"net/http"
	"strconv"

	"orchestrator/internal/api"
	mw "orchestrator/internal/middleware"

	"clearsky/contracts/topology"

	"github.com/gin-gonic/gin"
)

// HandleListInstitutions lists registered institutions (public, names only).
func HandleListInstitutions(c *gin.Context, m Messenger) {
	var list []map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyInstitutions, map[string]any{"type": "list"}, &list); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, list)
}

// HandleRegisterInstitution registers or updates the representative's own
// institution (SRS 2.2).
func HandleRegisterInstitution(c *gin.Context, m Messenger) {
	var req struct {
		Name         string `json:"name" binding:"required"`
		ContactEmail string `json:"contact_email"`
		Email        string `json:"email"` // accepted for older clients
		Director     string `json:"director"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The institution name and contact email are required", nil)
		return
	}
	if req.ContactEmail == "" {
		req.ContactEmail = req.Email
	}
	var inst map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyInstitutions, map[string]any{
		"type": "register", "institution_id": mw.GetInstitutionID(c), "actor_user_id": mw.GetUserID(c),
		"name": req.Name, "contact_email": req.ContactEmail, "director": req.Director,
	}, &inst); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, inst)
}

// HandleMyInstitution returns the institution and its credits.
func HandleMyInstitution(c *gin.Context, m Messenger) {
	var inst map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyInstitutions, map[string]any{
		"type": "get", "institution_id": mw.GetInstitutionID(c)}, &inst); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, inst)
}

// HandlePurchaseCredits buys credits for the representative's institution
// (SRS 2.4). An Idempotency-Key header makes a retried request harmless.
func HandlePurchaseCredits(c *gin.Context, m Messenger) {
	var req struct {
		Amount int `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "A number of credits is required", nil)
		return
	}
	var balance map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyInstitutions, map[string]any{
		"type": "purchase", "institution_id": mw.GetInstitutionID(c), "actor_user_id": mw.GetUserID(c),
		"amount": req.Amount, "idempotency_key": c.GetHeader("Idempotency-Key"),
	}, &balance); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, balance)
}

// HandleCreditHistory lists the latest credit movements.
func HandleCreditHistory(c *gin.Context, m Messenger) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	var entries []map[string]any
	if err := callJSON(c.Request.Context(), m, topology.KeyInstitutions, map[string]any{
		"type": "history", "institution_id": mw.GetInstitutionID(c), "limit": limit}, &entries); err != nil {
		serviceError(c, err)
		return
	}
	api.Success(c, http.StatusOK, entries)
}
