package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const requestIDKey = "request_id"

type ErrorCode string

const (
	CodeInvalidRequest      ErrorCode = "INVALID_REQUEST"
	CodeUnauthenticated     ErrorCode = "UNAUTHENTICATED"
	CodeForbidden           ErrorCode = "FORBIDDEN"
	CodeNotFound            ErrorCode = "NOT_FOUND"
	CodeConflict            ErrorCode = "CONFLICT"
	CodeRateLimited         ErrorCode = "RATE_LIMITED"
	CodeInsufficientCredits ErrorCode = "INSUFFICIENT_CREDITS"
	CodeServiceUnavailable  ErrorCode = "SERVICE_UNAVAILABLE"
	CodeServiceTimeout      ErrorCode = "SERVICE_TIMEOUT"
	CodeInvalidServiceReply ErrorCode = "INVALID_SERVICE_RESPONSE"
	CodeInternal            ErrorCode = "INTERNAL_ERROR"
)

type ErrorBody struct {
	Code    ErrorCode         `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type Envelope struct {
	Data      any        `json:"data,omitempty"`
	Error     *ErrorBody `json:"error,omitempty"`
	RequestID string     `json:"request_id"`
}

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.NewString()
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func RequestID(c *gin.Context) string {
	if id := c.GetString(requestIDKey); id != "" {
		return id
	}
	id := uuid.NewString()
	c.Set(requestIDKey, id)
	c.Header("X-Request-ID", id)
	return id
}

func Success(c *gin.Context, status int, data any) {
	c.JSON(status, Envelope{Data: data, RequestID: RequestID(c)})
}

func Failure(c *gin.Context, status int, code ErrorCode, message string, fields map[string]string) {
	c.JSON(status, Envelope{
		Error:     &ErrorBody{Code: code, Message: message, Fields: fields},
		RequestID: RequestID(c),
	})
}

func Abort(c *gin.Context, status int, code ErrorCode, message string) {
	Failure(c, status, code, message, nil)
	c.Abort()
}

func Internal(c *gin.Context) {
	Failure(c, http.StatusInternalServerError, CodeInternal, "An internal error occurred", nil)
}
