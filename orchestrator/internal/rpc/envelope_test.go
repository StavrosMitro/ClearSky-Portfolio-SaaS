package rpc

import (
	"errors"
	"testing"
)

func TestDecodeSuccess(t *testing.T) {
	var data struct {
		Credits int `json:"credits"`
	}
	if err := Decode([]byte(`{"version":1,"data":{"credits":7}}`), &data); err != nil {
		t.Fatal(err)
	}
	if data.Credits != 7 {
		t.Fatalf("credits=%d", data.Credits)
	}
}

func TestDecodeRemoteError(t *testing.T) {
	err := Decode([]byte(`{"version":1,"error":{"code":"CONFLICT","message":"duplicate","retryable":false}}`), nil)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.RPCError.Code != "CONFLICT" {
		t.Fatalf("error=%v", err)
	}
}

func TestDecodeRejectsAmbiguousEnvelope(t *testing.T) {
	err := Decode([]byte(`{"version":1,"data":{},"error":{"code":"CONFLICT","message":"duplicate","retryable":false}}`), nil)
	if err == nil {
		t.Fatal("accepted an envelope containing both data and error")
	}
}

func TestDecodeRejectsMissingVersion(t *testing.T) {
	err := Decode([]byte(`{"status":"ok","data":{}}`), nil)
	if err == nil {
		t.Fatal("accepted a legacy response without an RPC version")
	}
}
