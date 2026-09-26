package messaging

import "errors"

var (
	ErrClosed            = errors.New("rabbitmq client unavailable")
	ErrOverloaded        = errors.New("rabbitmq client overloaded")
	ErrUnroutable        = errors.New("rabbitmq command unroutable")
	ErrBrokerNack        = errors.New("rabbitmq broker rejected publish")
	ErrConnectionLost    = errors.New("rabbitmq connection lost")
	ErrMalformedResponse = errors.New("rabbitmq malformed response")
)
