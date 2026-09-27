// Package rpc implements the versioned RabbitMQ RPC envelope (v1):
//
//	{"version":1,"data":{...}}
//	{"version":1,"error":{"code":"CONFLICT","message":"...","retryable":false}}
//
// data and error are mutually exclusive. Messages are safe for end users.
package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const Version = 1

// Stable error codes shared by all producers.
const (
	CodeInvalidRequest        = "INVALID_REQUEST"
	CodeInvalidCredentials    = "INVALID_CREDENTIALS"
	CodeUnauthenticated       = "UNAUTHENTICATED"
	CodeForbidden             = "FORBIDDEN"
	CodeNotFound              = "NOT_FOUND"
	CodeConflict              = "CONFLICT"
	CodeInsufficientCredits   = "INSUFFICIENT_CREDITS"
	CodeDependencyUnavailable = "DEPENDENCY_UNAVAILABLE"
	CodeInternal              = "INTERNAL_ERROR"
)

// Error is a failure whose code and message may be shown to clients.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Fail returns a non-retryable error.
func Fail(code, message string) *Error { return &Error{Code: code, Message: message} }

// Unavailable returns a retryable DEPENDENCY_UNAVAILABLE error.
func Unavailable(message string) *Error {
	return &Error{Code: CodeDependencyUnavailable, Message: message, Retryable: true}
}

// AsError returns err as *Error when it is one (or wraps one).
func AsError(err error) (*Error, bool) {
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		return rpcErr, true
	}
	return nil, false
}

type envelope struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Success encodes a success envelope. A nil result becomes an empty object,
// because a success must carry data.
func Success(data any) ([]byte, error) {
	if data == nil {
		data = struct{}{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Version: Version, Data: raw})
}

// Failure encodes an error envelope.
func Failure(e *Error) []byte {
	raw, _ := json.Marshal(envelope{Version: Version, Error: e})
	return raw
}

// RemoteError is a well-formed error envelope received from another service.
type RemoteError struct {
	RPCError Error
}

func (e *RemoteError) Error() string { return e.RPCError.Code }

// Decode validates an envelope and unmarshals its data into out (which may be
// nil). An error envelope becomes *RemoteError; anything malformed is a plain
// error, which callers treat as an invalid service response.
func Decode(raw []byte, out any) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode RPC envelope: %w", err)
	}
	if env.Version != Version {
		return fmt.Errorf("unsupported RPC envelope version %d", env.Version)
	}
	if env.Error != nil {
		if env.Error.Code == "" || env.Error.Message == "" || len(env.Data) != 0 {
			return errors.New("invalid RPC error envelope")
		}
		return &RemoteError{RPCError: *env.Error}
	}
	if len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) {
		return errors.New("RPC success envelope is missing data")
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// Bind decodes a request body into v; malformed JSON is INVALID_REQUEST.
func Bind(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return Fail(CodeInvalidRequest, "The request is malformed")
	}
	return nil
}
