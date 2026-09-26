package grove

import (
	"context"
	"sync/atomic"
)

// Lease is one node's claim on an exclusive cluster capability. Runtimes
// implement it; a lease is fenced, so Held turns false as soon as ownership
// has moved or the lease could no longer be renewed.
type Lease interface {
	// Held reports whether the holder may still act as the exclusive owner.
	Held() bool
	// Release gives up the capability.
	Release()
}

// ExclusiveProvider grants exclusive capability ownership. Runtime packages
// implement it and attach it to handler contexts with WithExclusiveProvider.
type ExclusiveProvider interface {
	// AcquireExclusive returns a lease for capability, or a lease that is
	// never held when another placement owns it.
	AcquireExclusive(ctx context.Context, capability string) (Lease, error)
}

type exclusiveProviderKey struct{}

// WithExclusiveProvider returns ctx carrying provider for Exclusive.
func WithExclusiveProvider(ctx context.Context, provider ExclusiveProvider) context.Context {
	return context.WithValue(ctx, exclusiveProviderKey{}, provider)
}

// Ownership is a workload's claim on a named exclusive capability.
type Ownership struct {
	lease Lease
	ctx   context.Context
}

// Exclusive claims the cluster-wide capability name for the calling workload.
// Loop on Enabled: it turns false once ownership is lost or released, so a
// stale owner stops acting. Without a runtime provider in ctx, the caller is
// the sole owner for as long as ctx lives, keeping business code unit-testable
// without Grove.
func Exclusive(ctx context.Context, name string) *Ownership {
	provider, ok := ctx.Value(exclusiveProviderKey{}).(ExclusiveProvider)
	if !ok {
		return &Ownership{ctx: ctx, lease: &localLease{ctx: ctx}}
	}
	lease, err := provider.AcquireExclusive(ctx, name)
	if err != nil || lease == nil {
		return &Ownership{ctx: ctx, lease: deniedLease{}}
	}
	return &Ownership{ctx: ctx, lease: lease}
}

// Enabled reports whether this workload currently owns the capability.
func (o *Ownership) Enabled() bool { return o.ctx.Err() == nil && o.lease.Held() }

// Release gives up ownership.
func (o *Ownership) Release() { o.lease.Release() }

type deniedLease struct{}

func (deniedLease) Held() bool { return false }
func (deniedLease) Release()   {}

type localLease struct {
	ctx      context.Context
	released atomic.Bool
}

func (l *localLease) Held() bool { return l.ctx.Err() == nil && !l.released.Load() }
func (l *localLease) Release()   { l.released.Store(true) }
