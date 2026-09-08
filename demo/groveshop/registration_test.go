package groveshop_test

import (
	"errors"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
)

// Application registrations must dispatch each stable ID pair to the intended
// concrete Grove Shop implementation.
func TestGroveShopRegistration(t *testing.T) {
	registry := &grove.Registry{}
	inventory := &groveshop.Inventory{}
	payment := &groveshop.Payment{}
	shipping := &groveshop.Shipping{}
	orders := groveshop.NewOrders(inventory, payment, shipping)

	for _, register := range []func() error{
		func() error { return groveshop.RegisterOrders(registry, orders) },
		func() error { return groveshop.RegisterInventory(registry, inventory) },
		func() error { return groveshop.RegisterPayment(registry, payment) },
		func() error { return groveshop.RegisterShipping(registry, shipping) },
	} {
		if err := register(); err != nil {
			t.Fatal(err)
		}
	}

	reserve, err := registry.Resolve(groveshop.ServiceInventory, groveshop.MethodReserve)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := reserve(t.Context(), groveshop.ReserveRequest{
		OrderID:  "order-1",
		SKU:      "coffee-beans",
		Quantity: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := reserved.(groveshop.Reservation).ID; got != "reservation-order-1" {
		t.Errorf("Inventory handler reservation ID = %q; want reservation-order-1", got)
	}

	charge, err := registry.Resolve(groveshop.ServicePayment, groveshop.MethodCharge)
	if err != nil {
		t.Fatal(err)
	}
	charged, err := charge(t.Context(), groveshop.ChargeRequest{OrderID: "order-1", AmountCents: 2400})
	if err != nil {
		t.Fatal(err)
	}
	if got := charged.(groveshop.PaymentResult).ID; got != "payment-order-1" {
		t.Errorf("Payment handler payment ID = %q; want payment-order-1", got)
	}

	ship, err := registry.Resolve(groveshop.ServiceShipping, groveshop.MethodArrangeShipping)
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := ship(t.Context(), groveshop.ShippingRequest{OrderID: "order-1", Address: "12 Grove Lane"})
	if err != nil {
		t.Fatal(err)
	}
	if got := shipped.(groveshop.Shipment).ID; got != "shipment-order-1" {
		t.Errorf("Shipping handler shipment ID = %q; want shipment-order-1", got)
	}

	create, err := registry.Resolve(groveshop.ServiceOrders, groveshop.MethodCreateOrder)
	if err != nil {
		t.Fatal(err)
	}
	created, err := create(t.Context(), groveshop.CreateOrderRequest{
		OrderID:         "order-1",
		SKU:             "coffee-beans",
		Quantity:        2,
		AmountCents:     2400,
		ShippingAddress: "12 Grove Lane",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.(groveshop.Order).Status; got != groveshop.OrderCompleted {
		t.Errorf("Orders handler status = %q; want %q", got, groveshop.OrderCompleted)
	}

	if _, err := reserve(t.Context(), groveshop.ChargeRequest{}); !errors.Is(err, groveshop.ErrHandlerRequestType) {
		t.Errorf("Inventory handler request error = %v; want %v", err, groveshop.ErrHandlerRequestType)
	}
}
