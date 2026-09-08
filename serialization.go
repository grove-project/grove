package grove

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
)

// CodecOperation identifies the serialization operation that failed.
type CodecOperation string

const (
	// CodecEncode identifies a failed Encode operation.
	CodecEncode CodecOperation = "encode"
	// CodecDecode identifies a failed Decode operation.
	CodecDecode CodecOperation = "decode"
)

var (
	// ErrDecodeTargetRequired is returned when Decode receives a nil output
	// pointer.
	ErrDecodeTargetRequired = errors.New("decode target is required")
)

// CodecError reports a failed Gob operation without exposing Gob throughout
// application code.
type CodecError struct {
	// Operation identifies whether encoding or decoding failed.
	Operation CodecOperation
	// Err is the underlying serialization failure.
	Err error
}

func (e *CodecError) Error() string {
	return fmt.Sprintf("%s Grove value: %v", e.Operation, e.Err)
}

func (e *CodecError) Unwrap() error {
	return e.Err
}

// Encode serializes value with the Grove MVP Gob codec.
func Encode[Value any](value Value) ([]byte, error) {
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(value); err != nil {
		return nil, &CodecError{Operation: CodecEncode, Err: err}
	}
	return encoded.Bytes(), nil
}

// Decode deserializes a Grove MVP Gob payload into out.
func Decode[Value any](data []byte, out *Value) error {
	if out == nil {
		return &CodecError{Operation: CodecDecode, Err: ErrDecodeTargetRequired}
	}
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(out); err != nil {
		return &CodecError{Operation: CodecDecode, Err: err}
	}
	return nil
}
