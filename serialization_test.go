package grove_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/grove-project/grove"
)

func TestEncodeAndDecode(t *testing.T) {
	type payload struct {
		OrderID  string
		Quantity int
	}
	want := payload{OrderID: "order-1", Quantity: 2}
	encoded, err := grove.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	var got payload
	if err := grove.Decode(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decode(Encode(value)) = %#v; want %#v", got, want)
	}

	if _, err := grove.Encode(make(chan int)); err == nil {
		t.Error("Encode() accepted a channel")
	} else {
		var codecErr *grove.CodecError
		if !errors.As(err, &codecErr) || codecErr.Operation != grove.CodecEncode {
			t.Errorf("Encode() error = %v; want encode CodecError", err)
		}
	}
	if err := grove.Decode([]byte("not Gob"), &got); err == nil {
		t.Error("Decode() accepted malformed input")
	} else {
		var codecErr *grove.CodecError
		if !errors.As(err, &codecErr) || codecErr.Operation != grove.CodecDecode {
			t.Errorf("Decode() malformed error = %v; want decode CodecError", err)
		}
	}
	if err := grove.Decode[payload](encoded, nil); !errors.Is(err, grove.ErrDecodeTargetRequired) {
		t.Errorf("Decode() nil target error = %v; want %v", err, grove.ErrDecodeTargetRequired)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	payload, err := grove.Encode("coffee-beans")
	if err != nil {
		t.Fatal(err)
	}
	request := grove.RequestEnvelope{
		RequestID: "request-42",
		ServiceID: 2,
		MethodID:  1,
		Payload:   payload,
	}
	encodedRequest, err := grove.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	var decodedRequest grove.RequestEnvelope
	if err := grove.Decode(encodedRequest, &decodedRequest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodedRequest, request) {
		t.Errorf("request envelope round trip = %#v; want %#v", decodedRequest, request)
	}

	response := grove.ResponseEnvelope{
		RequestID: request.RequestID,
		Error: &grove.ResponseError{
			Code:    grove.ErrorHandler,
			Message: "inventory failed",
		},
	}
	encodedResponse, err := grove.Encode(response)
	if err != nil {
		t.Fatal(err)
	}
	var decodedResponse grove.ResponseEnvelope
	if err := grove.Decode(encodedResponse, &decodedResponse); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodedResponse, response) {
		t.Errorf("response envelope round trip = %#v; want %#v", decodedResponse, response)
	}
	if decodedResponse.RequestID != request.RequestID {
		t.Errorf("response request ID = %q; want %q", decodedResponse.RequestID, request.RequestID)
	}
}
