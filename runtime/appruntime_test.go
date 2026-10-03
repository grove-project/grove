package runtime

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

// A node runs its placed components in one shared application runtime
// process, not one process per component, and keeps explicitly isolated
// components in dedicated workers. Handler, shared-runtime and isolated-worker
// failures each affect exactly the components inside their boundary.
func TestGrovletHostsComponentsInOneApplicationRuntime(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("process tree inspection requires /proc")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	const (
		nodeSubject = "_GROVE.system.shared.node-1"
		nodeID      = "node-1"
	)
	cluster := startMembershipGrovlets(
		t, ctx,
		[]string{
			"--system-nats-subject", nodeSubject, "--system-nats-recovery",
			"--component", "orders", "--component", "inventory", "--component", "payment", "--component", "shipping",
			"--component-option", "orders=distributed",
			"--component-isolate", "shipping",
		},
		[]string{"--system-nats-subject", "_GROVE.system.shared.node-2", "--system-nats-recovery"},
		[]string{"--system-nats-subject", "_GROVE.system.shared.node-3", "--system-nats-recovery"},
	)
	shared := []grove.ServiceID{groveshop.ServiceOrders, groveshop.ServiceInventory, groveshop.ServicePayment}
	all := append(append([]grove.ServiceID{}, shared...), groveshop.ServiceShipping)
	desired := systemnats.DesiredDeployment{
		ApplicationID:  "grove-shop",
		Version:        "current",
		ArtifactDigest: grovletArtifactDigest(t),
	}
	for _, serviceID := range all {
		desired.Components = append(desired.Components, systemnats.DesiredComponent{ServiceID: serviceID, NodeID: nodeID})
	}
	if err := cluster.transports[2].PutDesired(ctx, nodeID, desired); err != nil {
		t.Fatalf("write desired deployment: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	initial := waitForHealthyComponents(t, ctx, cluster, nodeID, all, nil)
	var placement []systemnats.PlacementRecord
	for _, serviceID := range all {
		placement = append(placement, systemnats.PlacementRecord{
			ServiceID: serviceID, NodeID: nodeID, InvocationSubject: componentInvocationSubject(nodeSubject, serviceID), ArtifactDigest: grovletArtifactDigest(t),
		})
	}
	if _, err := waitForGrovletPlacement(ctx, cluster, placement); err != nil {
		t.Fatalf("wait for placement: %v\n%s", err, clusterLogs(cluster.nodes))
	}

	runtimeProcess := initial[groveshop.ServiceOrders]
	if runtimeProcess.ExecutionMode != systemnats.ExecutionInProcess || runtimeProcess.ProcessID == "" || runtimeProcess.PID == 0 {
		t.Fatalf("Orders execution = %q %q pid %d; want the shared application runtime", runtimeProcess.ExecutionMode, runtimeProcess.ProcessID, runtimeProcess.PID)
	}
	for _, serviceID := range shared {
		component := initial[serviceID]
		if component.ExecutionMode != systemnats.ExecutionInProcess || component.ProcessID != runtimeProcess.ProcessID || component.PID != runtimeProcess.PID {
			t.Errorf("%s execution = %q %q pid %d; want in-process in %s pid %d",
				component.Name, component.ExecutionMode, component.ProcessID, component.PID, runtimeProcess.ProcessID, runtimeProcess.PID)
		}
	}
	isolated := initial[groveshop.ServiceShipping]
	if isolated.ExecutionMode != systemnats.ExecutionIsolatedProcess || isolated.PID == 0 || isolated.PID == runtimeProcess.PID || isolated.ProcessID == runtimeProcess.ProcessID {
		t.Errorf("Shipping execution = %q %q pid %d; want a dedicated worker", isolated.ExecutionMode, isolated.ProcessID, isolated.PID)
	}
	grovletPID := parentPID(t, runtimeProcess.PID)
	if got := parentPID(t, isolated.PID); got != grovletPID {
		t.Errorf("isolated worker parent = %d; want Grovlet %d", got, grovletPID)
	}
	// Four components, two application processes: the shared runtime and the
	// one isolated worker.
	if children := childPIDs(t, grovletPID); len(children) != 2 {
		t.Errorf("Grovlet %d children = %v; want the application runtime and one isolated worker", grovletPID, children)
	}
	placeOrder(t, ctx, cluster, nodeSubject, "shared-runtime-order")

	// Handler failure: one in-process component ends; its neighbours and the
	// shared process keep running and it restarts in the same process.
	if _, err := cluster.transports[0].RequestKillComponent(ctx, nodeID, groveshop.ServiceInventory); err != nil {
		t.Fatalf("kill Inventory: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	restarted, err := waitForGrovletComponentGeneration(ctx, cluster.transports[1], nodeID, groveshop.ServiceInventory, initial[groveshop.ServiceInventory].Generation)
	if err != nil {
		t.Fatalf("wait for restarted Inventory: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if restarted.PID != runtimeProcess.PID || restarted.ProcessID != runtimeProcess.ProcessID {
		t.Errorf("restarted Inventory runs in %s pid %d; want the unchanged runtime %s pid %d", restarted.ProcessID, restarted.PID, runtimeProcess.ProcessID, runtimeProcess.PID)
	}
	afterHandler := componentsByService(t, ctx, cluster.transports[1], nodeID)
	for _, serviceID := range []grove.ServiceID{groveshop.ServiceOrders, groveshop.ServicePayment, groveshop.ServiceShipping} {
		if afterHandler[serviceID].Generation != initial[serviceID].Generation {
			t.Errorf("%s generation = %d after Inventory failure; want unchanged %d", afterHandler[serviceID].Name, afterHandler[serviceID].Generation, initial[serviceID].Generation)
		}
	}

	// Shared runtime failure: every in-process component fails with the
	// process and recovers in a new runtime; the isolated worker is outside
	// that boundary.
	if err := syscall.Kill(runtimeProcess.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill application runtime: %v", err)
	}
	recovered := waitForHealthyComponents(t, ctx, cluster, nodeID, shared, afterHandler)
	replacement := recovered[groveshop.ServiceOrders]
	if replacement.PID == runtimeProcess.PID || replacement.ProcessID == runtimeProcess.ProcessID {
		t.Errorf("recovered runtime = %s pid %d; want a new process, not %s pid %d", replacement.ProcessID, replacement.PID, runtimeProcess.ProcessID, runtimeProcess.PID)
	}
	for _, serviceID := range shared {
		if recovered[serviceID].PID != replacement.PID {
			t.Errorf("%s recovered in pid %d; want shared pid %d", recovered[serviceID].Name, recovered[serviceID].PID, replacement.PID)
		}
	}
	if shipping := componentsByService(t, ctx, cluster.transports[1], nodeID)[groveshop.ServiceShipping]; shipping.Generation != initial[groveshop.ServiceShipping].Generation || shipping.PID != isolated.PID {
		t.Errorf("isolated Shipping = generation %d pid %d after runtime failure; want unchanged %d pid %d", shipping.Generation, shipping.PID, initial[groveshop.ServiceShipping].Generation, isolated.PID)
	}
	placeOrder(t, ctx, cluster, nodeSubject, "recovered-runtime-order")

	// Isolated worker failure affects only the isolated component.
	if err := syscall.Kill(isolated.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill isolated worker: %v", err)
	}
	if _, err := waitForGrovletComponentGeneration(ctx, cluster.transports[1], nodeID, groveshop.ServiceShipping, initial[groveshop.ServiceShipping].Generation); err != nil {
		t.Fatalf("wait for restarted Shipping: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	afterWorker := componentsByService(t, ctx, cluster.transports[1], nodeID)
	for _, serviceID := range shared {
		if afterWorker[serviceID].Generation != recovered[serviceID].Generation || afterWorker[serviceID].PID != replacement.PID {
			t.Errorf("%s = generation %d pid %d after isolated worker failure; want unchanged %d pid %d",
				afterWorker[serviceID].Name, afterWorker[serviceID].Generation, afterWorker[serviceID].PID, recovered[serviceID].Generation, replacement.PID)
		}
	}
	placeOrder(t, ctx, cluster, nodeSubject, "recovered-worker-order")

	for i := len(cluster.nodes) - 1; i >= 0; i-- {
		if err := cluster.nodes[i].Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, pid := range []int{replacement.PID, afterWorker[groveshop.ServiceShipping].PID} {
		if !processExits(ctx, pid) {
			t.Errorf("stopped Grovlet left application process %d running", pid)
		}
	}
}

// processExits waits until pid no longer runs.
func processExits(ctx context.Context, pid int) bool {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return true
		}
		if fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:])); len(fields) != 0 && fields[0] == "Z" {
			return true
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return false
		}
	}
}

// waitForHealthyComponents waits until every service is healthy on nodeID,
// in a newer generation than before when before names it.
func waitForHealthyComponents(
	t *testing.T,
	ctx context.Context,
	cluster membershipGrovlets,
	nodeID string,
	services []grove.ServiceID,
	before map[grove.ServiceID]systemnats.ComponentStatus,
) map[grove.ServiceID]systemnats.ComponentStatus {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last map[grove.ServiceID]systemnats.ComponentStatus
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		view, err := cluster.transports[1].RequestComponents(requestCtx, nodeID)
		cancel()
		if err == nil {
			last = map[grove.ServiceID]systemnats.ComponentStatus{}
			for _, component := range view.Components {
				last[component.ServiceID] = component
			}
			ready := true
			for _, serviceID := range services {
				component := last[serviceID]
				if component.State != systemnats.ComponentHealthy || component.Generation <= before[serviceID].Generation {
					ready = false
				}
			}
			if ready {
				return last
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("components %v did not become healthy on %s: %#v: %v\n%s", services, nodeID, last, ctx.Err(), clusterLogs(cluster.nodes))
		}
	}
}

func componentsByService(t *testing.T, ctx context.Context, transport *systemnats.Transport, nodeID string) map[grove.ServiceID]systemnats.ComponentStatus {
	t.Helper()
	view, err := transport.RequestComponents(ctx, nodeID)
	if err != nil {
		t.Fatalf("request %s components: %v", nodeID, err)
	}
	components := make(map[grove.ServiceID]systemnats.ComponentStatus, len(view.Components))
	for _, component := range view.Components {
		components[component.ServiceID] = component
	}
	return components
}

func placeOrder(t *testing.T, ctx context.Context, cluster membershipGrovlets, nodeSubject, orderID string) {
	t.Helper()
	client, err := cluster.transports[2].RoutedClient(componentInvocationSubject(nodeSubject, groveshop.ServiceOrders))
	if err != nil {
		t.Fatal(err)
	}
	created, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: orderID, SKU: "coffee", Quantity: 1, AmountCents: 900, ShippingAddress: "19 Grove Lane",
	})
	if err != nil || created.Status != groveshop.OrderCompleted {
		t.Fatalf("order %s = %#v, %v\n%s", orderID, created, err, clusterLogs(cluster.nodes))
	}
}

func parentPID(t *testing.T, pid int) int {
	t.Helper()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("read process %d: %v", pid, err)
	}
	// The command name is parenthesized and may contain spaces; the parent
	// PID is the second field after it.
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("parse process %d parent: %v", pid, err)
	}
	return ppid
}

// childPIDs lists the live child processes of pid.
func childPIDs(t *testing.T, pid int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var children []int
	for _, entry := range entries {
		child, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child))
		if err != nil {
			continue
		}
		rest := string(stat[strings.LastIndexByte(string(stat), ')')+1:])
		fields := strings.Fields(rest)
		// Zombies are exited processes awaiting reaping, not running workers.
		if len(fields) < 2 || fields[0] == "Z" {
			continue
		}
		if ppid, err := strconv.Atoi(fields[1]); err == nil && ppid == pid {
			children = append(children, child)
		}
	}
	return children
}
