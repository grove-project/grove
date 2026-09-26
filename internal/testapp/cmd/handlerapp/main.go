// Command handlerapp is the Grove test application with handler-level
// placement declared on its Payment component, used to prove automatic
// scaling and exclusive ownership across real Grovlet processes.
package main

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/testapp"
	"github.com/grove-project/grove/internal/testapp/runtimeapp"
	groveruntime "github.com/grove-project/grove/runtime"
)

const (
	// MethodWhoAmI is an ordinary handler: it answers with the executing node.
	MethodWhoAmI grove.MethodID = 100
	// MethodLoadGen is an exclusive handler whose workload runs in the
	// background on the single owner.
	MethodLoadGen grove.MethodID = 101
	// LoadGenCapability is the exclusive capability claimed by the workload.
	LoadGenCapability = "handlerapp/load-generator"
)

// LoadGenStatus reports one worker's view of the load generator.
type LoadGenStatus struct {
	Node   string
	Active bool
	Ticks  int64
}

func main() {
	definition := runtimeapp.RuntimeDefinition()
	for i := range definition.Components {
		component := &definition.Components[i]
		if component.ServiceID == testapp.ServiceWeb {
			addIngressProbes(component)
			continue
		}
		if component.ServiceID != testapp.ServicePayment {
			continue
		}
		register := component.Register
		component.Register = func(ctx groveruntime.ComponentContext) error {
			if err := register(ctx); err != nil {
				return err
			}
			return registerProbes(ctx)
		}
		component.Handlers = []groveruntime.HandlerSpec{
			{Method: testapp.MethodCharge, Name: "Charge"},
			{Method: MethodWhoAmI, Name: "WhoAmI"},
			{Method: MethodLoadGen, Name: "LoadGen", Exclusive: true, Capability: LoadGenCapability},
		}
	}
	groveruntime.Main(definition)
}

// addIngressProbes adds HTTP routes whose handlers call Payment through the
// same Grove client as any application code, so ingress traffic is placed by
// the same healthy handler placements as internal RPC.
func addIngressProbes(component *groveruntime.Component) {
	web := component.HTTPHandler
	component.HTTPHandler = func(ctx groveruntime.ComponentContext) (http.Handler, error) {
		base, err := web(ctx)
		if err != nil {
			return nil, err
		}
		mux := http.NewServeMux()
		mux.Handle("/", base)
		mux.HandleFunc("GET /probe/whoami", func(w http.ResponseWriter, r *http.Request) {
			node, err := grove.Call[struct{}, string](r.Context(), ctx.Client, testapp.ServicePayment, MethodWhoAmI, struct{}{})
			writeProbe(w, node, err)
		})
		mux.HandleFunc("GET /probe/loadgen", func(w http.ResponseWriter, r *http.Request) {
			status, err := grove.Call[struct{}, LoadGenStatus](r.Context(), ctx.Client, testapp.ServicePayment, MethodLoadGen, struct{}{})
			writeProbe(w, status.Node, err)
		})
		return mux, nil
	}
	component.Routes = append(component.Routes,
		groveruntime.Route{Method: "GET", Path: "/probe/whoami"},
		groveruntime.Route{Method: "GET", Path: "/probe/loadgen"},
	)
}

func writeProbe(w http.ResponseWriter, node string, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	_, _ = io.WriteString(w, node)
}

func registerProbes(ctx groveruntime.ComponentContext) error {
	node := "unknown"
	if len(ctx.Options) != 0 {
		node = ctx.Options[0]
	}
	if err := ctx.Registry.Register(testapp.ServicePayment, MethodWhoAmI, func(context.Context, []byte) ([]byte, error) {
		return grove.Encode(node)
	}); err != nil {
		return err
	}
	var active atomic.Bool
	var ticks atomic.Int64
	// The load generator runs only while this worker owns the capability.
	go func() {
		for ctx.Context.Err() == nil {
			ownership := grove.Exclusive(ctx.Context, LoadGenCapability)
			for ownership.Enabled() {
				active.Store(true)
				ticks.Add(1)
				time.Sleep(20 * time.Millisecond)
			}
			active.Store(false)
			ownership.Release()
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return ctx.Registry.Register(testapp.ServicePayment, MethodLoadGen, func(context.Context, []byte) ([]byte, error) {
		return grove.Encode(LoadGenStatus{Node: node, Active: active.Load(), Ticks: ticks.Load()})
	})
}
