package handlers

import (
	"clearsky/contracts/rpc"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"orchestrator/internal/api"
	"orchestrator/internal/messaging"

	"github.com/gin-gonic/gin"
)

type Messenger interface {
	Call(context.Context, string, []byte) ([]byte, error)
	Send(context.Context, string, []byte) error
	Ready() bool
}

func callJSON(ctx context.Context, m Messenger, key string, request any, response any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	reply, err := m.Call(ctx, key, body)
	if err != nil {
		return err
	}
	return decodeRPCReply(reply, response)
}

func decodeRPCReply(reply []byte, response any) error {
	if err := rpc.Decode(reply, response); err != nil {
		var remote *rpc.RemoteError
		if errors.As(err, &remote) {
			return remote
		}
		return fmt.Errorf("%w: %v", messaging.ErrMalformedResponse, err)
	}
	return nil
}
func publishJSON(ctx context.Context, m Messenger, key string, request any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return m.Send(ctx, key, body)
}
func messagingError(c *gin.Context, err error) {
	var remote *rpc.RemoteError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		api.Failure(c, http.StatusGatewayTimeout, api.CodeServiceTimeout, "A required service timed out", nil)
	case errors.Is(err, context.Canceled):
		// The client has gone away; attempting to write a non-standard 499
		// response only creates another write failure.
		c.Abort()
	case errors.Is(err, messaging.ErrMalformedResponse):
		api.Failure(c, http.StatusBadGateway, api.CodeInvalidServiceReply, "A required service returned an invalid response", nil)
	case errors.Is(err, messaging.ErrOverloaded):
		api.Failure(c, http.StatusServiceUnavailable, api.CodeServiceUnavailable, "The service is busy; try again later", nil)
	case errors.As(err, &remote):
		writeRPCError(c, remote.RPCError)
	default:
		api.Failure(c, http.StatusServiceUnavailable, api.CodeServiceUnavailable, "A required service is unavailable", nil)
	}
}

func writeRPCError(c *gin.Context, rpcErr rpc.Error) {
	switch rpcErr.Code {
	case "INVALID_REQUEST":
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid", nil)
	case "INVALID_CREDENTIALS", "UNAUTHENTICATED":
		api.Failure(c, http.StatusUnauthorized, api.CodeUnauthenticated, "Invalid credentials", nil)
	case "FORBIDDEN":
		api.Failure(c, http.StatusForbidden, api.CodeForbidden, "You are not allowed to perform this operation", nil)
	case "NOT_FOUND":
		api.Failure(c, http.StatusNotFound, api.CodeNotFound, "The requested resource was not found", nil)
	case "CONFLICT", "INSUFFICIENT_CREDITS":
		api.Failure(c, http.StatusConflict, api.CodeConflict, rpcErr.Message, nil)
	default:
		if rpcErr.Retryable {
			api.Failure(c, http.StatusServiceUnavailable, api.CodeServiceUnavailable, "A required service is temporarily unavailable", nil)
			return
		}
		api.Failure(c, http.StatusBadGateway, api.CodeInvalidServiceReply, "A required service rejected the operation", nil)
	}
}

// serviceError maps an error from a service that writes user-facing
// messages (identity, institutions, grades, reviews): the message of a
// client error is shown as is; anything else stays generic.
func serviceError(c *gin.Context, err error) {
	var remote *rpc.RemoteError
	if !errors.As(err, &remote) {
		messagingError(c, err)
		return
	}
	message := remote.RPCError.Message
	switch remote.RPCError.Code {
	case rpc.CodeInvalidRequest:
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, message, nil)
	case rpc.CodeUnauthenticated, rpc.CodeInvalidCredentials:
		api.Failure(c, http.StatusUnauthorized, api.CodeUnauthenticated, message, nil)
	case rpc.CodeForbidden:
		api.Failure(c, http.StatusForbidden, api.CodeForbidden, message, nil)
	case rpc.CodeNotFound:
		api.Failure(c, http.StatusNotFound, api.CodeNotFound, message, nil)
	case rpc.CodeConflict:
		api.Failure(c, http.StatusConflict, api.CodeConflict, message, nil)
	case rpc.CodeInsufficientCredits:
		api.Failure(c, http.StatusConflict, api.CodeInsufficientCredits, message, nil)
	case rpc.CodeDependencyUnavailable:
		api.Failure(c, http.StatusServiceUnavailable, api.CodeServiceUnavailable, message, nil)
	default:
		messagingError(c, err)
	}
}
