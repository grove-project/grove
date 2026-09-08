package grove_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/grove-project/grove"
)

// A Registry resolves an application-owned ID pair to its explicit local
// implementation.
func ExampleRegistry() {
	var registry grove.Registry
	err := registry.Register(7, 3, func(_ context.Context, request []byte) ([]byte, error) {
		return append([]byte("handled "), request...), nil
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	handler, err := registry.Resolve(7, 3)
	if err != nil {
		fmt.Println(err)
		return
	}
	response, err := handler(context.Background(), []byte("order"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(response))
	// Output:
	// handled order
}

func TestRegistryRegisterAndResolve(t *testing.T) {
	const (
		serviceID grove.ServiceID = 7
		methodID  grove.MethodID  = 3
	)
	var registry grove.Registry
	first := func(_ context.Context, _ []byte) ([]byte, error) { return []byte("first"), nil }
	second := func(_ context.Context, _ []byte) ([]byte, error) { return []byte("second"), nil }

	if err := registry.Register(serviceID, methodID, first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(serviceID, methodID, second); !errors.Is(err, grove.ErrDuplicateRegistration) {
		t.Errorf("duplicate Register() error = %v; want %v", err, grove.ErrDuplicateRegistration)
	}
	handler, err := registry.Resolve(serviceID, methodID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := handler(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Errorf("resolved handler response = %q; want first registration", got)
	}

	if err := registry.Register(serviceID, methodID+1, nil); !errors.Is(err, grove.ErrNilHandler) {
		t.Errorf("nil Register() error = %v; want %v", err, grove.ErrNilHandler)
	}
	if _, err := registry.Resolve(serviceID+1, methodID); !errors.Is(err, grove.ErrUnknownService) {
		t.Errorf("Resolve() unknown service error = %v; want %v", err, grove.ErrUnknownService)
	}
	if _, err := registry.Resolve(serviceID, methodID+1); !errors.Is(err, grove.ErrUnknownMethod) {
		t.Errorf("Resolve() unknown method error = %v; want %v", err, grove.ErrUnknownMethod)
	}
}
