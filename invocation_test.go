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
	err := registry.Register(2, 1, func(_ context.Context, request any) (any, error) {
		return request.(string) + " reserved", nil
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
	if _, err := grove.NewClient(nil); !errors.Is(err, grove.ErrRegistryRequired) {
		t.Errorf("NewClient() nil registry error = %v; want %v", err, grove.ErrRegistryRequired)
	}
	if err := registry.Register(serviceID, methodID, func(ctx context.Context, request any) (any, error) {
		if got := ctx.Value(contextKey{}); got != "context value" {
			t.Errorf("handler context value = %v; want context value", got)
		}
		return request.(string) + " response", nil
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
	}
	if _, err := grove.Call[string, string](ctx, client, serviceID, methodID+1, "request"); !errors.Is(err, grove.ErrUnknownMethod) {
		t.Errorf("Call() unknown method error = %v; want %v", err, grove.ErrUnknownMethod)
	}

	handlerErr := errors.New("handler failed")
	if err := registry.Register(serviceID, methodID+1, func(context.Context, any) (any, error) {
		return nil, handlerErr
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := grove.Call[string, string](ctx, client, serviceID, methodID+1, "request"); !errors.Is(err, handlerErr) {
		t.Errorf("Call() handler error = %v; want %v", err, handlerErr)
	}

	if err := registry.Register(serviceID, methodID+2, func(context.Context, any) (any, error) {
		return 42, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := grove.Call[string, string](ctx, client, serviceID, methodID+2, "request"); !errors.Is(err, grove.ErrResponseType) {
		t.Errorf("Call() response error = %v; want %v", err, grove.ErrResponseType)
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
