package grove

import (
	"errors"
	"fmt"
)

var (
	// ErrRequestIDMismatch is returned when a response does not correlate to its
	// request.
	ErrRequestIDMismatch = errors.New("response request ID does not match request")
)

// ErrorCode classifies a failed invocation independently of its transport.
type ErrorCode string

const (
	// ErrorDispatch identifies service or method resolution failure.
	ErrorDispatch ErrorCode = "dispatch"
	// ErrorHandler identifies an application handler failure.
	ErrorHandler ErrorCode = "handler"
	// ErrorSerialization identifies request or response serialization failure.
	ErrorSerialization ErrorCode = "serialization"
	// ErrorTransport identifies a failure exchanging an invocation with its
	// destination.
	ErrorTransport ErrorCode = "transport"
)

// RequestEnvelope carries one serialized invocation independently of network
// transport.
type RequestEnvelope struct {
	// RequestID correlates this request with its response.
	RequestID string
	// ServiceID identifies the requested application service.
	ServiceID ServiceID
	// MethodID identifies the requested application method.
	MethodID MethodID
	// Payload contains the encoded application request.
	Payload []byte
}

// ResponseEnvelope carries either a serialized response payload or a
// structured invocation error.
type ResponseEnvelope struct {
	// RequestID correlates this response with its request.
	RequestID string
	// Payload contains the encoded application response on success.
	Payload []byte
	// Error describes a failed invocation. It is nil on success.
	Error *ResponseError
}

// ResponseError is the transport-independent representation of an invocation
// failure. Code and Message survive serialization; a local response also
// unwraps to its original cause.
type ResponseError struct {
	// Code classifies the invocation failure.
	Code ErrorCode
	// Message describes the failure for diagnostics.
	Message string

	cause error
}

func (e *ResponseError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s failure: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s failure", e.Code)
}

func (e *ResponseError) Unwrap() error {
	return e.cause
}

func responseError(code ErrorCode, err error) *ResponseError {
	return &ResponseError{Code: code, Message: err.Error(), cause: err}
}
