package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const Version = 1

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type RemoteError struct {
	RPCError Error
}

func (e *RemoteError) Error() string { return e.RPCError.Code }

type envelope struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
	Error   *Error          `json:"error"`
}

// Decode validates the v1 RPC envelope and unmarshals its data.
func Decode(raw []byte, out any) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode RPC envelope: %w", err)
	}
	if env.Version == Version {
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
	return fmt.Errorf("unsupported RPC envelope version %d", env.Version)
}
