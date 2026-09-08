package grove_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
)

var grovletPath string

// Call keeps the service boundary and application-owned types visible at the
// call site.
func ExampleCall() {
	var registry grove.Registry
	err := registry.Register(2, 1, func(_ context.Context, payload []byte) ([]byte, error) {
		var request string
		if err := grove.Decode(payload, &request); err != nil {
			return nil, err
		}
		return grove.Encode(request + " reserved")
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	client, err := grove.NewClient(&registry)
	if err != nil {
		fmt.Println(err)
		return
	}
	response, err := grove.Call[string, string](context.Background(), client, 2, 1, "coffee")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(response)
	// Output:
	// coffee reserved
}

func TestCall(t *testing.T) {
	type contextKey struct{}
	const (
		serviceID grove.ServiceID = 7
		methodID  grove.MethodID  = 3
	)
	var registry grove.Registry
	if _, err := grove.NewRoutedClient(nil); !errors.Is(err, grove.ErrRouterRequired) {
		t.Errorf("NewRoutedClient() nil router error = %v; want %v", err, grove.ErrRouterRequired)
	}
	if _, err := grove.NewClient(nil); !errors.Is(err, grove.ErrRegistryRequired) {
		t.Errorf("NewClient() nil registry error = %v; want %v", err, grove.ErrRegistryRequired)
	}
	if err := registry.Register(serviceID, methodID, func(ctx context.Context, payload []byte) ([]byte, error) {
		if got := ctx.Value(contextKey{}); got != "context value" {
			t.Errorf("handler context value = %v; want context value", got)
		}
		var request string
		if err := grove.Decode(payload, &request); err != nil {
			return nil, err
		}
		return grove.Encode(request + " response")
	}); err != nil {
		t.Fatal(err)
	}
	client, err := grove.NewClient(&registry)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), contextKey{}, "context value")
	got, err := grove.Call[string, string](ctx, client, serviceID, methodID, "request")
	if err != nil {
		t.Fatal(err)
	}
	if got != "request response" {
		t.Errorf("Call() = %q; want request response", got)
	}

	if _, err := grove.Call[string, string](ctx, client, serviceID+1, methodID, "request"); !errors.Is(err, grove.ErrUnknownService) {
		t.Errorf("Call() unknown service error = %v; want %v", err, grove.ErrUnknownService)
	} else {
		checkResponseCode(t, err, grove.ErrorDispatch)
	}
	if _, err := grove.Call[string, string](ctx, client, serviceID, methodID+1, "request"); !errors.Is(err, grove.ErrUnknownMethod) {
		t.Errorf("Call() unknown method error = %v; want %v", err, grove.ErrUnknownMethod)
	} else {
		checkResponseCode(t, err, grove.ErrorDispatch)
	}

	handlerErr := errors.New("handler failed")
	if err := registry.Register(serviceID, methodID+1, func(context.Context, []byte) ([]byte, error) {
		return nil, handlerErr
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := grove.Call[string, string](ctx, client, serviceID, methodID+1, "request"); !errors.Is(err, handlerErr) {
		t.Errorf("Call() handler error = %v; want %v", err, handlerErr)
	} else {
		checkResponseCode(t, err, grove.ErrorHandler)
	}

	if err := registry.Register(serviceID, methodID+2, func(_ context.Context, payload []byte) ([]byte, error) {
		var number int
		if err := grove.Decode(payload, &number); err != nil {
			return nil, err
		}
		return grove.Encode(number)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := grove.Call[string, int](ctx, client, serviceID, methodID+2, "request"); err == nil {
		t.Error("Call() accepted handler serialization failure")
	} else {
		checkResponseCode(t, err, grove.ErrorSerialization)
	}

	if err := registry.Register(serviceID, methodID+3, func(context.Context, []byte) ([]byte, error) {
		return []byte("not a Gob response"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := grove.Call[string, string](ctx, client, serviceID, methodID+3, "request"); err == nil {
		t.Error("Call() accepted malformed response payload")
	} else {
		var codecErr *grove.CodecError
		if !errors.As(err, &codecErr) || codecErr.Operation != grove.CodecDecode {
			t.Errorf("Call() malformed response error = %v; want decode CodecError", err)
		}
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := grove.Call[string, string](canceled, client, serviceID, methodID, "request"); !errors.Is(err, context.Canceled) {
		t.Errorf("Call() context error = %v; want %v", err, context.Canceled)
	}
	if _, err := grove.Call[string, string](ctx, nil, serviceID, methodID, "request"); !errors.Is(err, grove.ErrClientRequired) {
		t.Errorf("Call() nil client error = %v; want %v", err, grove.ErrClientRequired)
	}
}

func checkResponseCode(t *testing.T, err error, want grove.ErrorCode) {
	t.Helper()
	var responseErr *grove.ResponseError
	if !errors.As(err, &responseErr) {
		t.Errorf("Call() error type = %T; want *grove.ResponseError", err)
		return
	}
	if responseErr.Code != want {
		t.Errorf("Call() response error code = %q; want %q", responseErr.Code, want)
	}
}

// The runtime-shaped application flow uses the same local invocation boundary
// while a real Grovlet is alive and ready.
func TestCallRunsGroveShopLocally(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	node, err := grovetest.StartNode(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if err := node.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}

	var registry grove.Registry
	inventory := &groveshop.Inventory{}
	if err := groveshop.RegisterInventory(&registry, inventory); err != nil {
		t.Fatal(err)
	}
	client, err := grove.NewClient(&registry)
	if err != nil {
		t.Fatal(err)
	}
	orders := groveshop.NewGroveOrders(client, &groveshop.Payment{}, &groveshop.Shipping{})
	created, err := orders.Create(ctx, groveshop.CreateOrderRequest{
		OrderID:         "order-local",
		SKU:             "coffee-beans",
		Quantity:        2,
		AmountCents:     2400,
		ShippingAddress: "12 Grove Lane",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != groveshop.OrderCompleted {
		t.Errorf("local Grove order status = %q; want %q", created.Status, groveshop.OrderCompleted)
	}
	if created.Reservation.ID != "reservation-order-local" {
		t.Errorf("local Grove reservation ID = %q; want reservation-order-local", created.Reservation.ID)
	}

	if err := node.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	buildDir, err := os.MkdirTemp("", "grove-invocation-build-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	path, buildErr := grovetest.BuildGrovlet(ctx, buildDir)
	cancel()
	if buildErr != nil {
		fmt.Fprintln(os.Stderr, buildErr)
		_ = os.RemoveAll(buildDir)
		os.Exit(1)
	}
	grovletPath = path

	code := m.Run()
	if err := os.RemoveAll(buildDir); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
