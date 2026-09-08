package groveshop_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/grove-project/grove/demo/groveshop"
)

// Grove Shop stays usable as ordinary Go without starting the Grove runtime.
func Example() {
	orders := groveshop.NewOrders(
		&groveshop.Inventory{},
		&groveshop.Payment{},
		&groveshop.Shipping{},
	)
	web := groveshop.NewWeb(orders)

	created, err := web.CreateOrder(context.Background(), groveshop.CreateOrderRequest{
		OrderID:         "order-1",
		SKU:             "coffee-beans",
		Quantity:        2,
		AmountCents:     2400,
		ShippingAddress: "12 Grove Lane",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	stored, err := web.GetOrder(context.Background(), created.ID)
	if err != nil {
		fmt.Println(err)
		return
	}
	recent, err := web.ListOrders(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(created.History)
	fmt.Println(stored.ID, len(recent))
	// Output:
	// [Created Reserved Paid Shipping Completed]
	// order-1 1
}

func TestInventoryReserve(t *testing.T) {
	inventory := &groveshop.Inventory{}
	req := groveshop.ReserveRequest{OrderID: "order-1", SKU: "coffee-beans", Quantity: 2}
	want := groveshop.Reservation{
		ID:       "reservation-order-1",
		OrderID:  "order-1",
		SKU:      "coffee-beans",
		Quantity: 2,
	}
	for range 2 {
		got, err := inventory.Reserve(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Reserve() = %#v; want %#v", got, want)
		}
	}
}

func TestPaymentCharge(t *testing.T) {
	payment := &groveshop.Payment{}
	req := groveshop.ChargeRequest{OrderID: "order-1", AmountCents: 2400}
	want := groveshop.PaymentResult{
		ID:          "payment-order-1",
		OrderID:     "order-1",
		AmountCents: 2400,
	}
	for range 2 {
		got, err := payment.Charge(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Charge() = %#v; want %#v", got, want)
		}
	}
}

func TestShippingArrange(t *testing.T) {
	shipping := &groveshop.Shipping{}
	req := groveshop.ShippingRequest{OrderID: "order-1", Address: "12 Grove Lane"}
	want := groveshop.Shipment{
		ID:      "shipment-order-1",
		OrderID: "order-1",
		Address: "12 Grove Lane",
	}
	for range 2 {
		got, err := shipping.Arrange(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Arrange() = %#v; want %#v", got, want)
		}
	}
}

func TestNewOrdersRejectsNilService(t *testing.T) {
	tests := []struct {
		name      string
		inventory *groveshop.Inventory
		payment   *groveshop.Payment
		shipping  *groveshop.Shipping
	}{
		{name: "inventory", payment: &groveshop.Payment{}, shipping: &groveshop.Shipping{}},
		{name: "payment", inventory: &groveshop.Inventory{}, shipping: &groveshop.Shipping{}},
		{name: "shipping", inventory: &groveshop.Inventory{}, payment: &groveshop.Payment{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("NewOrders() did not panic")
				}
			}()
			groveshop.NewOrders(test.inventory, test.payment, test.shipping)
		})
	}
}

func TestOrdersCreate(t *testing.T) {
	orders := groveshop.NewOrders(
		&groveshop.Inventory{},
		&groveshop.Payment{},
		&groveshop.Shipping{},
	)
	req := groveshop.CreateOrderRequest{
		OrderID:         "order-1",
		SKU:             "coffee-beans",
		Quantity:        2,
		AmountCents:     2400,
		ShippingAddress: "12 Grove Lane",
	}
	got, err := orders.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantHistory := []groveshop.OrderStatus{
		groveshop.OrderCreated,
		groveshop.OrderReserved,
		groveshop.OrderPaid,
		groveshop.OrderShipping,
		groveshop.OrderCompleted,
	}
	if got.Status != groveshop.OrderCompleted {
		t.Errorf("Create() status = %q; want %q", got.Status, groveshop.OrderCompleted)
	}
	if !reflect.DeepEqual(got.History, wantHistory) {
		t.Errorf("Create() history = %v; want %v", got.History, wantHistory)
	}
	if got.Reservation.ID != "reservation-order-1" ||
		got.Payment.ID != "payment-order-1" ||
		got.Shipment.ID != "shipment-order-1" {
		t.Errorf("Create() component results = %#v, %#v, %#v; want deterministic IDs", got.Reservation, got.Payment, got.Shipment)
	}

	got.History[0] = groveshop.OrderCompleted
	stored, err := orders.Get(t.Context(), req.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.History, wantHistory) {
		t.Errorf("Get() history = %v; want isolated snapshot %v", stored.History, wantHistory)
	}
	listed, err := orders.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != req.OrderID {
		t.Errorf("List() = %#v; want order %q", listed, req.OrderID)
	}
	if _, err := orders.Get(t.Context(), "missing"); !errors.Is(err, groveshop.ErrOrderNotFound) {
		t.Errorf("Get() missing order error = %v; want %v", err, groveshop.ErrOrderNotFound)
	}
	if _, err := orders.Create(t.Context(), req); !errors.Is(err, groveshop.ErrOrderExists) {
		t.Errorf("duplicate Create() error = %v; want %v", err, groveshop.ErrOrderExists)
	}
}

func TestOrdersCreatePropagatesComponentErrors(t *testing.T) {
	orders := groveshop.NewOrders(
		&groveshop.Inventory{},
		&groveshop.Payment{},
		&groveshop.Shipping{},
	)
	tests := []struct {
		name    string
		req     groveshop.CreateOrderRequest
		wantErr error
	}{
		{
			name: "inventory",
			req: groveshop.CreateOrderRequest{
				OrderID: "bad-inventory",
				SKU:     "coffee-beans",
			},
			wantErr: groveshop.ErrQuantityPositive,
		},
		{
			name: "payment",
			req: groveshop.CreateOrderRequest{
				OrderID:  "bad-payment",
				SKU:      "coffee-beans",
				Quantity: 1,
			},
			wantErr: groveshop.ErrAmountPositive,
		},
		{
			name: "shipping",
			req: groveshop.CreateOrderRequest{
				OrderID:     "bad-shipping",
				SKU:         "coffee-beans",
				Quantity:    1,
				AmountCents: 1200,
			},
			wantErr: groveshop.ErrShippingAddressRequired,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := orders.Create(t.Context(), test.req); !errors.Is(err, test.wantErr) {
				t.Errorf("Create() error = %v; want %v", err, test.wantErr)
			}
		})
	}

	got, err := orders.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("List() after failed orders = %#v; want no retained orders", got)
	}
}

func TestNewWebRejectsNilOrders(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewWeb() did not panic")
		}
	}()
	groveshop.NewWeb(nil)
}
