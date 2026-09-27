package systemnats

import (
	"context"
	"sync"

	"github.com/grove-project/grove"
	"github.com/nats-io/nats.go"
)

// Endpoint is one invocation endpoint served by the process that owns a
// Transport. Remote callers reach it over System NATS; calls made through the
// same Transport are dispatched in-process, because the selected destination
// already runs in the caller's application runtime.
type Endpoint struct {
	transport    *Transport
	subject      string
	subscription *nats.Subscription
	stopOnce     sync.Once
	stopErr      error
}

// ServeEndpoint serves handler on subject like Serve and additionally routes
// this Transport's own requests for subject to handler without leaving the
// process. Stop withdraws both paths.
func (t *Transport) ServeEndpoint(ctx context.Context, subject string, handler Handler) (*Endpoint, error) {
	subscription, err := t.subscribe(ctx, subject, handler)
	if err != nil {
		return nil, err
	}
	t.localMu.Lock()
	if t.local == nil {
		t.local = make(map[string]Handler)
	}
	t.local[subject] = handler
	t.localMu.Unlock()
	return &Endpoint{transport: t, subject: subject, subscription: subscription}, nil
}

// Stop removes the endpoint from in-process routing and unsubscribes it from
// System NATS. Calls already dispatched keep running.
func (e *Endpoint) Stop() error {
	e.stopOnce.Do(func() {
		e.transport.localMu.Lock()
		delete(e.transport.local, e.subject)
		e.transport.localMu.Unlock()
		if err := e.subscription.Unsubscribe(); err != nil {
			e.stopErr = &Error{Operation: "unsubscribe System NATS endpoint", Err: err}
		}
	})
	return e.stopErr
}

// LocalEndpoint reports whether subject is served in this Transport's process
// and so is invoked in-process.
func (t *Transport) LocalEndpoint(subject string) bool {
	_, ok := t.localHandler(subject)
	return ok
}

func (t *Transport) localHandler(subject string) (Handler, bool) {
	t.localMu.RLock()
	defer t.localMu.RUnlock()
	handler, ok := t.local[subject]
	return handler, ok
}

// dispatchLocal invokes an in-process endpoint with the same envelope and
// correlation semantics as a System NATS request.
func dispatchLocal(ctx context.Context, handler Handler, request grove.RequestEnvelope) grove.ResponseEnvelope {
	response := handler(ctx, request)
	response.RequestID = request.RequestID
	return response
}
