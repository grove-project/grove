package groveshop

import (
	"context"
	"errors"
	"fmt"

	"github.com/grove-project/grove"
)

const (
	// ServiceOrders identifies the Grove Shop Orders service.
	ServiceOrders grove.ServiceID = 1
	// ServiceInventory identifies the Grove Shop Inventory service.
	ServiceInventory grove.ServiceID = 2
	// ServicePayment identifies the Grove Shop Payment service.
	ServicePayment grove.ServiceID = 3
	// ServiceShipping identifies the Grove Shop Shipping service.
	ServiceShipping grove.ServiceID = 4
)

const (
	// MethodCreateOrder identifies Orders.Create.
	MethodCreateOrder grove.MethodID = 1
	// MethodReserve identifies Inventory.Reserve.
	MethodReserve grove.MethodID = 1
	// MethodCharge identifies Payment.Charge.
	MethodCharge grove.MethodID = 1
	// MethodArrangeShipping identifies Shipping.Arrange.
	MethodArrangeShipping grove.MethodID = 1
)

var (
	// ErrRegistryRequired is returned when registration receives a nil Registry.
	ErrRegistryRequired = errors.New("registry is required")
	// ErrServiceRequired is returned when registration receives a nil concrete
	// service implementation.
	ErrServiceRequired = errors.New("service implementation is required")
	// ErrHandlerRequestType is returned when a registered handler receives the
	// wrong application request type.
	ErrHandlerRequestType = errors.New("unexpected handler request type")
)

// RegisterOrders explicitly associates Orders.Create with the Grove Shop IDs.
func RegisterOrders(registry *grove.Registry, orders *Orders) error {
	if registry == nil {
		return ErrRegistryRequired
	}
	if orders == nil {
		return ErrServiceRequired
	}
	return registry.Register(
		ServiceOrders,
		MethodCreateOrder,
		func(ctx context.Context, request any) (any, error) {
			req, ok := request.(CreateOrderRequest)
			if !ok {
				return nil, fmt.Errorf("create order: %w", ErrHandlerRequestType)
			}
			return orders.Create(ctx, req)
		},
	)
}

// RegisterInventory explicitly associates Inventory.Reserve with the Grove
// Shop IDs.
func RegisterInventory(registry *grove.Registry, inventory *Inventory) error {
	if registry == nil {
		return ErrRegistryRequired
	}
	if inventory == nil {
		return ErrServiceRequired
	}
	return registry.Register(
		ServiceInventory,
		MethodReserve,
		func(ctx context.Context, request any) (any, error) {
			req, ok := request.(ReserveRequest)
			if !ok {
				return nil, fmt.Errorf("reserve inventory: %w", ErrHandlerRequestType)
			}
			return inventory.Reserve(ctx, req)
		},
	)
}

// RegisterPayment explicitly associates Payment.Charge with the Grove Shop
// IDs.
func RegisterPayment(registry *grove.Registry, payment *Payment) error {
	if registry == nil {
		return ErrRegistryRequired
	}
	if payment == nil {
		return ErrServiceRequired
	}
	return registry.Register(
		ServicePayment,
		MethodCharge,
		func(ctx context.Context, request any) (any, error) {
			req, ok := request.(ChargeRequest)
			if !ok {
				return nil, fmt.Errorf("charge payment: %w", ErrHandlerRequestType)
			}
			return payment.Charge(ctx, req)
		},
	)
}

// RegisterShipping explicitly associates Shipping.Arrange with the Grove Shop
// IDs.
func RegisterShipping(registry *grove.Registry, shipping *Shipping) error {
	if registry == nil {
		return ErrRegistryRequired
	}
	if shipping == nil {
		return ErrServiceRequired
	}
	return registry.Register(
		ServiceShipping,
		MethodArrangeShipping,
		func(ctx context.Context, request any) (any, error) {
			req, ok := request.(ShippingRequest)
			if !ok {
				return nil, fmt.Errorf("arrange shipping: %w", ErrHandlerRequestType)
			}
			return shipping.Arrange(ctx, req)
		},
	)
}
