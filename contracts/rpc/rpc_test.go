package rpc

import (
	"errors"
	"fmt"
	"testing"
)

func TestSuccessAndFailureRoundTrip(t *testing.T) {
	raw, err := Success(map[string]int{"credits": 3})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Credits int }
	if err := Decode(raw, &out); err != nil || out.Credits != 3 {
		t.Fatalf("decode success: %+v, %v", out, err)
	}
	if raw, _ := Success(nil); Decode(raw, nil) != nil {
		t.Fatal("a nil result must still be a valid success")
	}

	err = Decode(Failure(Fail(CodeConflict, "Already registered")), nil)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.RPCError.Code != CodeConflict || remote.RPCError.Message != "Already registered" {
		t.Fatalf("decode failure: %v", err)
	}
}

func TestDecodeRejectsMalformedEnvelopes(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":        `{`,
		"no version":      `{"data":{}}`,
		"wrong version":   `{"version":2,"data":{}}`,
		"missing data":    `{"version":1}`,
		"null data":       `{"version":1,"data":null}`,
		"both fields":     `{"version":1,"data":{},"error":{"code":"X","message":"y"}}`,
		"error sans code": `{"version":1,"error":{"message":"y"}}`,
	} {
		err := Decode([]byte(raw), nil)
		var remote *RemoteError
		if err == nil || errors.As(err, &remote) {
			t.Errorf("%s: err = %v, want a malformed-envelope error", name, err)
		}
	}
}

func TestAsErrorUnwraps(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", Unavailable("db down"))
	rpcErr, ok := AsError(wrapped)
	if !ok || rpcErr.Code != CodeDependencyUnavailable || !rpcErr.Retryable {
		t.Fatalf("AsError = %+v, %v", rpcErr, ok)
	}
}
