package systemnats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/placement"
	"github.com/nats-io/nats.go"
)

const (
	handlerViewSubjectRoot  = "_GROVE.system.handlers.view."
	handlerLeaseSubjectRoot = "_GROVE.system.handlers.lease."
)

// Resolved returns the observed view restricted to live nodes: placements
// with no live node are dropped, so every consumer routes over the same
// healthy set.
func (h *HandlerPlacements) Resolved() HandlerPlacementView {
	view := h.Snapshot()
	live, ready := h.cfg.Live()
	if !ready {
		view.Ready = false
		return view
	}
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[id] = true
	}
	resolved := view.Placements[:0]
	for _, p := range view.Placements {
		nodes := p.Nodes[:0]
		for _, n := range p.Nodes {
			if liveSet[n.NodeID] {
				nodes = append(nodes, n)
			}
		}
		if len(nodes) != 0 {
			p.Nodes = nodes
			resolved = append(resolved, p)
		}
	}
	view.Placements = resolved
	return view
}

// ServeHandlerPlacement registers nodeID's endpoint for its resolved handler
// placement view, used by the worker processes it hosts.
func (t *Transport) ServeHandlerPlacement(ctx context.Context, nodeID string, h *HandlerPlacements) error {
	if h == nil {
		return &Error{Operation: "serve Grove handler placement", Err: ErrPlacementRequired}
	}
	if _, err := t.connection.Subscribe(handlerViewSubjectRoot+nodeID, func(message *nats.Msg) {
		if encoded, err := json.Marshal(h.Resolved()); err == nil {
			_ = message.Respond(encoded)
		}
	}); err != nil {
		return &Error{Operation: "subscribe Grove handler placement endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove handler placement endpoint", Err: err}
	}
	return nil
}

// RequestHandlerPlacement requests nodeID's resolved handler placement view.
func (t *Transport) RequestHandlerPlacement(ctx context.Context, nodeID string) (HandlerPlacementView, error) {
	message, err := t.connection.RequestWithContext(ctx, handlerViewSubjectRoot+nodeID, nil)
	if err != nil {
		return HandlerPlacementView{}, &Error{Operation: "request Grove handler placement", Err: err}
	}
	var view HandlerPlacementView
	if err := json.Unmarshal(message.Data, &view); err != nil {
		return HandlerPlacementView{}, &Error{Operation: "decode Grove handler placement", Err: err}
	}
	return view, nil
}

// ObservedHandlerRouter routes each call through nodeID's resolved handler
// view, balancing across the handler's placements. Handlers with no placement
// go to fallback, which keeps whole-service placement working unchanged.
func (t *Transport) ObservedHandlerRouter(nodeID string, fallback grove.Router) grove.Router {
	return &observedHandlerRouter{transport: t, nodeID: nodeID, fallback: fallback}
}

type observedHandlerRouter struct {
	transport *Transport
	nodeID    string
	fallback  grove.Router
	selector  placement.Selector
}

func (r *observedHandlerRouter) Route(ctx context.Context, request grove.RequestEnvelope) (grove.ResponseEnvelope, error) {
	view, err := r.transport.RequestHandlerPlacement(ctx, r.nodeID)
	if err != nil || !view.Ready {
		return r.fallback.Route(ctx, request)
	}
	for _, p := range view.Placements {
		if p.Service != request.ServiceID || p.Method != request.MethodID {
			continue
		}
		ids := p.nodeIDs()
		target, _ := r.selector.Pick(p.id(), ids)
		for _, n := range p.Nodes {
			if n.NodeID != target {
				continue
			}
			response, err := r.transport.Request(ctx, n.InvocationSubject, request)
			if err != nil {
				return grove.ResponseEnvelope{}, fmt.Errorf("request: %w: %w", grove.ErrTransportFailure, err)
			}
			return response, nil
		}
	}
	return r.fallback.Route(ctx, request)
}

// leaseRequest and leaseReply are the worker <-> Grovlet lease proxy protocol.
type leaseRequest struct {
	Op         string `json:"op"` // acquire, renew, release
	Capability string `json:"capability,omitempty"`
	Token      string `json:"token,omitempty"`
}

type leaseReply struct {
	Status string `json:"status"` // granted, pending, denied, held, lost
	Token  string `json:"token,omitempty"`
}

type proxiedLease struct {
	lease    grove.Lease
	lastSeen time.Time
}

// ServeExclusiveLeases lets worker processes hosted by nodeID claim exclusive
// capabilities through the Grovlet, which holds the actual fenced lease. A
// proxied lease not renewed by its worker within LeaseTTL is released, so a
// dead worker cannot pin ownership.
func (t *Transport) ServeExclusiveLeases(ctx context.Context, nodeID string, h *HandlerPlacements) error {
	if h == nil {
		return &Error{Operation: "serve Grove exclusive leases", Err: ErrPlacementRequired}
	}
	var mu sync.Mutex
	leases := make(map[string]*proxiedLease)
	reply := func(message *nats.Msg, r leaseReply) {
		if encoded, err := json.Marshal(r); err == nil {
			_ = message.Respond(encoded)
		}
	}
	if _, err := t.connection.Subscribe(handlerLeaseSubjectRoot+nodeID, func(message *nats.Msg) {
		var request leaseRequest
		if json.Unmarshal(message.Data, &request) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		now := h.cfg.Now()
		for token, l := range leases {
			if now.Sub(l.lastSeen) >= h.cfg.LeaseTTL {
				l.lease.Release()
				delete(leases, token)
			}
		}
		switch request.Op {
		case "acquire":
			lease, done, err := h.TryAcquireExclusive(ctx, request.Capability)
			switch {
			case err != nil || !done:
				reply(message, leaseReply{Status: "pending"})
			case !lease.Held():
				reply(message, leaseReply{Status: "denied"})
			default:
				raw := make([]byte, 16)
				_, _ = rand.Read(raw)
				token := hex.EncodeToString(raw)
				leases[token] = &proxiedLease{lease: lease, lastSeen: now}
				reply(message, leaseReply{Status: "granted", Token: token})
			}
		case "renew":
			l, ok := leases[request.Token]
			if !ok || !l.lease.Held() {
				reply(message, leaseReply{Status: "lost"})
				return
			}
			l.lastSeen = now
			reply(message, leaseReply{Status: "held"})
		case "release":
			if l, ok := leases[request.Token]; ok {
				l.lease.Release()
				delete(leases, request.Token)
			}
			reply(message, leaseReply{Status: "lost"})
		}
	}); err != nil {
		return &Error{Operation: "subscribe Grove exclusive lease endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove exclusive lease endpoint", Err: err}
	}
	return nil
}

// RemoteExclusiveProvider claims exclusive capabilities through the Grovlet
// nodeID that hosts this process. Its leases are held only while renewals keep
// succeeding, so a worker that loses its Grovlet stops acting.
type RemoteExclusiveProvider struct {
	transport *Transport
	nodeID    string
	ttl       time.Duration
	poll      time.Duration
	lifetime  context.Context
}

// NewRemoteExclusiveProvider creates a provider whose lease renewals run until
// lifetime ends. ttl must match the Grovlet's lease TTL.
func (t *Transport) NewRemoteExclusiveProvider(lifetime context.Context, nodeID string, ttl time.Duration) *RemoteExclusiveProvider {
	if ttl <= 0 {
		ttl = defaultHandlerLeaseTTL
	}
	return &RemoteExclusiveProvider{transport: t, nodeID: nodeID, ttl: ttl, poll: ttl / 6, lifetime: lifetime}
}

func (p *RemoteExclusiveProvider) call(ctx context.Context, request leaseRequest) (leaseReply, error) {
	encoded, _ := json.Marshal(request)
	message, err := p.transport.connection.RequestWithContext(ctx, handlerLeaseSubjectRoot+p.nodeID, encoded)
	if err != nil {
		return leaseReply{}, err
	}
	var r leaseReply
	return r, json.Unmarshal(message.Data, &r)
}

// AcquireExclusive implements grove.ExclusiveProvider.
func (p *RemoteExclusiveProvider) AcquireExclusive(ctx context.Context, capability string) (grove.Lease, error) {
	ticker := time.NewTicker(p.poll)
	defer ticker.Stop()
	for {
		r, err := p.call(ctx, leaseRequest{Op: "acquire", Capability: capability})
		switch {
		case err == nil && r.Status == "granted":
			return p.startRenewing(r.Token), nil
		case err == nil && r.Status == "denied":
			return deniedHandle{}, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type remoteLease struct {
	mu       sync.Mutex
	held     bool
	lastOK   time.Time
	ttl      time.Duration
	stop     context.CancelFunc
	release  func()
	released sync.Once
}

func (l *remoteLease) Held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Fail safe: a worker that cannot confirm ownership recently stops acting.
	return l.held && time.Since(l.lastOK) < l.ttl/2
}

func (l *remoteLease) Release() {
	l.mu.Lock()
	l.held = false
	l.mu.Unlock()
	l.released.Do(func() {
		l.stop()
		l.release()
	})
}

func (p *RemoteExclusiveProvider) startRenewing(token string) *remoteLease {
	ctx, cancel := context.WithCancel(p.lifetime)
	lease := &remoteLease{held: true, lastOK: time.Now(), ttl: p.ttl, stop: cancel}
	lease.release = func() {
		releaseCtx, done := context.WithTimeout(context.WithoutCancel(p.lifetime), time.Second)
		defer done()
		_, _ = p.call(releaseCtx, leaseRequest{Op: "release", Token: token})
	}
	go func() {
		ticker := time.NewTicker(p.poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			callCtx, done := context.WithTimeout(ctx, p.poll)
			r, err := p.call(callCtx, leaseRequest{Op: "renew", Token: token})
			done()
			lease.mu.Lock()
			switch {
			case err == nil && r.Status == "held":
				lease.lastOK = time.Now()
			case err == nil:
				lease.held = false // the Grovlet no longer holds the capability
			}
			lease.mu.Unlock()
		}
	}()
	return lease
}
