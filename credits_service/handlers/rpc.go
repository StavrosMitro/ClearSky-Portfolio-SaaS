package handlers

type RPCError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type RPCEnvelope struct {
	Version int       `json:"version"`
	Data    any       `json:"data,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

func rpcSuccess(data any) RPCEnvelope { return RPCEnvelope{Version: 1, Data: data} }

func rpcFailure(code, message string, retryable bool) RPCEnvelope {
	return RPCEnvelope{Version: 1, Error: &RPCError{Code: code, Message: message, Retryable: retryable}}
}
