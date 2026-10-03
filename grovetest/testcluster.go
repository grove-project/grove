package grovetest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/controlplane"
	"github.com/grove-project/grove/internal/placement"
)

var (
	// ErrHandlerNotPlaced is returned by a TestNode's router when its observed
	// placement view has no destination for a handler.
	ErrHandlerNotPlaced = errors.New("handler is not placed")
	// ErrNodeUnreachable wraps grove.ErrTransportFailure when the in-memory
	// transport cannot deliver to a stopped, crashed, or partitioned node.
	ErrNodeUnreachable = fmt.Errorf("node unreachable: %w", grove.ErrTransportFailure)
)

const (
	defaultFailureDetection = 3 * time.Second
	maxConvergeRounds       = 32
)

// HandlerID names one placeable handler: an explicit service and method pair.
type HandlerID = placement.Handler

// NodeState is the lifecycle state of one logical node.
type NodeState string

const (
	// NodeAdded is a node that has not been started.
	NodeAdded NodeState = "added"
	// NodeRunning is a node that participates in the cluster.
	NodeRunning NodeState = "running"
	// NodeStopped is a node that left the cluster gracefully.
	NodeStopped NodeState = "stopped"
	// NodeCrashed is a node that vanished without announcing departure.
	NodeCrashed NodeState = "crashed"
)

// HandlerInfo describes one handler a node has registered.
type HandlerInfo struct {
	// Exclusive requests at most one owner cluster-wide.
	Exclusive bool
	// Capability names the exclusive capability the handler owns. It defaults
	// to the handler ID.
	Capability string
}

// NodeInfo is a policy-visible view of one live node.
type NodeInfo struct {
	ID       string
	Handlers map[HandlerID]HandlerInfo
}

// Topology is the input to a PlacementPolicy: the live nodes and the
// placements currently published.
type Topology struct {
	Nodes   []NodeInfo
	Current map[HandlerID][]string
}

// PlacementPolicy decides handler -> node placement from live topology. It is
// the seam through which Grove's production placement logic runs against the
// TestCluster. Implementations must be deterministic and must return node IDs
// sorted.
type PlacementPolicy interface {
	Place(Topology) map[HandlerID][]string
}

// PlacementPolicyFunc adapts a function to PlacementPolicy.
type PlacementPolicyFunc func(Topology) map[HandlerID][]string

// Place implements PlacementPolicy.
func (f PlacementPolicyFunc) Place(t Topology) map[HandlerID][]string { return f(t) }

// EverywherePolicy is Grove's production placer: automatic handlers run on
// every live node that registered them and each exclusive handler on exactly
// one, sticking with its current owner while eligible. It is the default.
type EverywherePolicy struct{}

// Place implements PlacementPolicy.
func (EverywherePolicy) Place(t Topology) map[HandlerID][]string {
	nodes := make([]placement.Node, len(t.Nodes))
	for i, n := range t.Nodes {
		nodes[i] = placement.Node{ID: n.ID, Handlers: make(map[HandlerID]placement.Scaling, len(n.Handlers))}
		for id, info := range n.Handlers {
			if info.Exclusive {
				nodes[i].Handlers[id] = placement.Exclusive
			} else {
				nodes[i].Handlers[id] = placement.Automatic
			}
		}
	}
	return placement.Place(placement.Topology{Nodes: nodes, Current: t.Current})
}

// Clock is the cluster's controllable time source.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the current test time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves test time forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// ClusterOption configures NewTestCluster.
type ClusterOption func(*TestCluster)

// WithPlacementPolicy replaces the default placement policy.
func WithPlacementPolicy(policy PlacementPolicy) ClusterOption {
	return func(c *TestCluster) { c.policy = policy }
}

// WithFailureDetection sets how long, in test-clock time, peers take to notice
// a crashed node.
func WithFailureDetection(d time.Duration) ClusterOption {
	return func(c *TestCluster) { c.detectAfter = d }
}

// HandlerOption configures one handler registration.
type HandlerOption func(*HandlerInfo)

// Exclusive marks a handler as single-owner.
func Exclusive() HandlerOption { return func(i *HandlerInfo) { i.Exclusive = true } }

// ExclusiveCapability marks a handler as single-owner and names the capability
// its workload claims with grove.Exclusive.
func ExclusiveCapability(name string) HandlerOption {
	return func(i *HandlerInfo) { i.Exclusive, i.Capability = true, name }
}

type registration struct {
	id      HandlerID
	handler grove.Handler
	info    HandlerInfo
}

// TestCluster runs several logical Grove nodes in one goroutine-free,
// deterministic in-process cluster. Each node owns a real grove.Registry,
// grove.Dispatcher, and routed grove.Client, and the cluster runs
// production's control-plane rules: health evaluation, handler placement
// reconciliation, and exclusive leasing. Only the store, the network, node
// processes, and the clock are simulated. Methods are for use by one test
// goroutine, though registered handlers may call back through node clients.
type TestCluster struct {
	tb          testing.TB
	clock       *Clock
	policy      PlacementPolicy
	detectAfter time.Duration
	leaseTTL    time.Duration

	mu         sync.Mutex
	nodes      []*TestNode
	records    map[HandlerID]controlplane.HandlerPlacement // authoritative placements
	store      map[HandlerID][]string                      // node IDs of records
	leases     map[string]placement.ObservedLease          // stored lease per capability
	partitions map[[2]string]bool
	calls      map[HandlerID]map[string]int
	events     []string
	rounds     int
}

// NewTestCluster creates an empty in-process cluster whose resources are
// released by tb's cleanup.
func NewTestCluster(tb testing.TB, opts ...ClusterOption) *TestCluster {
	tb.Helper()
	c := &TestCluster{
		tb:          tb,
		clock:       &Clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		policy:      EverywherePolicy{},
		detectAfter: defaultFailureDetection,
		store:       make(map[HandlerID][]string),
		records:     make(map[HandlerID]controlplane.HandlerPlacement),
		leases:      make(map[string]placement.ObservedLease),
		partitions:  make(map[[2]string]bool),
		calls:       make(map[HandlerID]map[string]int),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.leaseTTL == 0 {
		c.leaseTTL = c.detectAfter
	}
	tb.Cleanup(c.shutdown)
	return c
}

// Clock returns the cluster's controllable clock.
func (c *TestCluster) Clock() *Clock { return c.clock }

func (c *TestCluster) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		n.state = NodeStopped
	}
}

func (c *TestCluster) logf(format string, args ...any) {
	c.events = append(c.events, fmt.Sprintf("%s %s", c.clock.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...)))
}

// AddNode adds a logical node. Before Start the node stays added; afterwards it
// joins immediately and becomes routable after the next Converge.
func (c *TestCluster) AddNode() *TestNode {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := &TestNode{cluster: c, id: fmt.Sprintf("node-%d", len(c.nodes)+1), state: NodeAdded, registry: &grove.Registry{}}
	client, err := grove.NewRoutedClient(nodeRouter{node: n})
	if err != nil {
		c.tb.Fatalf("%s: %v", n.id, err)
	}
	n.client = client
	c.nodes = append(c.nodes, n)
	c.logf("%s added", n.id)
	if c.rounds > 0 {
		n.start()
	}
	return n
}

// Start starts every added node and converges.
func (c *TestCluster) Start() {
	c.tb.Helper()
	c.mu.Lock()
	for _, n := range c.nodes {
		if n.state == NodeAdded {
			n.start()
		}
	}
	c.rounds++
	c.mu.Unlock()
	c.Converge()
}

// StopNode gracefully removes n from the cluster.
func (c *TestCluster) StopNode(n *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n.state = NodeStopped
	n.leaving = true
	n.lastSeen = c.clock.Now()
	n.observed = nil
	c.logf("%s stopped gracefully", n.id)
}

// KillNode makes n vanish abruptly. Peers keep their stale placements until
// failure detection elapses during Converge.
func (c *TestCluster) KillNode(n *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n.state = NodeCrashed
	n.lastSeen = c.clock.Now()
	n.detected = false
	c.logf("%s crashed", n.id)
}

// RestartNode brings a stopped or crashed node back with fresh local state and
// its original handler registrations.
func (c *TestCluster) RestartNode(n *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n.start()
	c.logf("%s restarted (generation %d)", n.id, n.generation)
}

// Isolate cuts n off from the cluster while its process keeps running, as in a
// network partition around one node. Peers treat it as failed after failure
// detection; it can no longer renew exclusive leases, so it must stop acting
// as an exclusive owner once its lease expires.
func (c *TestCluster) Isolate(n *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n.isolated = true
	n.lastSeen = c.clock.Now()
	n.detected = false
	c.logf("%s isolated", n.id)
}

// Reconnect restores an isolated node's connectivity. As in production, its
// leases resume only while it is still the owner at the same epoch and nobody
// claimed them meanwhile; otherwise they stay fenced.
func (c *TestCluster) Reconnect(n *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n.isolated = false
	n.detected = false
	c.logf("%s reconnected", n.id)
}

// Partition blocks in-memory communication between a and b in both directions.
func (c *TestCluster) Partition(a, b *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partitions[edge(a.id, b.id)] = true
	c.logf("partition %s <-> %s", a.id, b.id)
}

// Heal removes every partition.
func (c *TestCluster) Heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partitions = make(map[[2]string]bool)
	c.logf("partitions healed")
}

func edge(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Converge advances test time past pending failure detection, then runs
// placement and publication until the cluster is stable. It fails the test with
// diagnostics if stability is not reached within a bounded number of rounds.
func (c *TestCluster) Converge() {
	c.tb.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.detectFailures()
	for round := 0; round < maxConvergeRounds; round++ {
		if !c.reconcileLocked() {
			return
		}
	}
	c.tb.Fatalf("cluster did not converge in %d rounds\n%s", maxConvergeRounds, c.diagnosticsLocked())
}

// healthLocked is the cluster's health view as production derives it
// (controlplane.EvaluateHealth): every started node is a member, a gracefully
// stopped one is leaving, and a reachable running node heartbeats
// continuously while a crashed or isolated one was last heard when it went
// silent.
func (c *TestCluster) healthLocked() controlplane.ClusterView {
	now := c.clock.Now()
	membership := controlplane.MembershipView{Ready: true}
	lastSeen := make(map[string]time.Time, len(c.nodes))
	for _, n := range c.nodes {
		if n.state == NodeAdded {
			continue
		}
		membership.Members = append(membership.Members, controlplane.MembershipRecord{NodeID: n.id, Leaving: n.leaving})
		lastSeen[n.id] = n.lastSeen
		if n.reachable() {
			lastSeen[n.id] = now
		}
	}
	return controlplane.EvaluateHealth(membership, lastSeen, now, c.detectAfter)
}

// detectFailures advances test time until health evaluation reports every
// silent node unavailable, as production's failure detection would.
func (c *TestCluster) detectFailures() {
	for _, n := range c.nodes {
		if n.state == NodeAdded || n.state == NodeStopped || n.reachable() || n.detected {
			continue
		}
		if wait := n.lastSeen.Add(c.detectAfter + time.Nanosecond).Sub(c.clock.Now()); wait > 0 {
			c.clock.Advance(wait)
		}
		for _, node := range c.healthLocked().Nodes {
			if node.NodeID == n.id && node.Health == controlplane.HealthHealthy {
				c.tb.Fatalf("%s still healthy after failure detection\n%s", n.id, c.diagnosticsLocked())
			}
		}
		n.detected = true
		n.observed = nil
		c.logf("%s failure detected", n.id)
	}
}

// place adapts the cluster's PlacementPolicy to production reconciliation.
func (c *TestCluster) place(t placement.Topology) map[HandlerID][]string {
	topology := Topology{Current: t.Current}
	for _, pn := range t.Nodes {
		info := NodeInfo{ID: pn.ID, Handlers: make(map[HandlerID]HandlerInfo, len(pn.Handlers))}
		for _, n := range c.nodes {
			if n.id != pn.ID {
				continue
			}
			for _, r := range n.regs {
				if _, ok := pn.Handlers[r.id]; ok {
					info.Handlers[r.id] = r.info
				}
			}
		}
		topology.Nodes = append(topology.Nodes, info)
	}
	return c.policy.Place(topology)
}

// reconcileLocked runs one production reconciliation step,
// controlplane.PlanHandlerPlacementsWith over the live nodes healthLocked
// reports, and publishes the result. It reports whether it changed any
// published or observed state.
func (c *TestCluster) reconcileLocked() bool {
	nodes := make(map[string]controlplane.NodeHandlers)
	for _, n := range c.nodes {
		if n.state != NodeRunning {
			continue
		}
		record := controlplane.NodeHandlers{NodeID: n.id}
		for _, r := range n.regs {
			record.Handlers = append(record.Handlers, controlplane.HandlerRegistration{
				Service: r.id.Service, Method: r.id.Method, Exclusive: r.info.Exclusive, Capability: capabilityName(r),
			})
		}
		nodes[n.id] = record
	}
	leaseEpochs := make(map[string]uint64, len(c.leases))
	for capability, l := range c.leases {
		leaseEpochs[capability] = l.Lease.Epoch
	}
	live := placement.LiveNodes(controlplane.Members(c.healthLocked()))
	plan := controlplane.PlanHandlerPlacementsWith(c.place, nodes, c.records, live, leaseEpochs)
	changed := len(plan.Writes) != 0 || len(plan.Deletes) != 0
	for _, write := range plan.Writes {
		c.records[write.Placement.Handler()] = write.Placement
	}
	for _, id := range plan.Deletes {
		delete(c.records, id)
	}
	if changed {
		c.store = make(map[HandlerID][]string, len(c.records))
		for id, record := range c.records {
			c.store[id] = record.NodeIDs()
		}
		c.logf("placements published: %s", formatPlacements(c.store))
	}
	c.renewLeasesLocked()
	for _, n := range c.nodes {
		if n.reachable() && !equalPlacements(n.observed, c.store) {
			n.observed = clonePlacements(c.store)
			changed = true
		}
	}
	return changed
}

// Nodes returns every node ever added, in creation order.
func (c *TestCluster) Nodes() []*TestNode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*TestNode(nil), c.nodes...)
}

// ActiveNodes returns running nodes.
func (c *TestCluster) ActiveNodes() []*TestNode {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*TestNode
	for _, n := range c.nodes {
		if n.state == NodeRunning {
			out = append(out, n)
		}
	}
	return out
}

// Placements returns the sorted IDs of the nodes hosting h in the
// authoritative placement store.
func (c *TestCluster) Placements(h HandlerID) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.store[h]...)
}

// AssertPlacements fails unless h is placed on exactly want.
func (c *TestCluster) AssertPlacements(h HandlerID, want ...*TestNode) {
	c.tb.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, len(want))
	for i, n := range want {
		ids[i] = n.id
	}
	sort.Strings(ids)
	if got := c.store[h]; !equalStrings(got, ids) {
		c.tb.Fatalf("handler %s placed on %v, want %v\n%s", h, got, ids, c.diagnosticsLocked())
	}
}

// Owner returns the single node hosting h, failing the test if h has no owner
// or several.
func (c *TestCluster) Owner(h HandlerID) *TestNode {
	c.tb.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	owners := c.store[h]
	if len(owners) != 1 {
		c.tb.Fatalf("handler %s has %d owners %v, want 1\n%s", h, len(owners), owners, c.diagnosticsLocked())
	}
	for _, n := range c.nodes {
		if n.id == owners[0] {
			return n
		}
	}
	c.tb.Fatalf("owner %s of %s is unknown\n%s", owners[0], h, c.diagnosticsLocked())
	return nil
}

// AssertSingleOwner fails unless exactly one running node is placed for h and
// no other node's observed view disagrees.
func (c *TestCluster) AssertSingleOwner(h HandlerID) {
	c.tb.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	owners := c.store[h]
	if len(owners) != 1 {
		c.tb.Fatalf("handler %s has %d owners %v, want 1\n%s", h, len(owners), owners, c.diagnosticsLocked())
	}
	for _, n := range c.nodes {
		if n.state == NodeRunning && !n.isolated && !equalStrings(n.observed[h], owners) {
			c.tb.Fatalf("%s observes owners %v for %s, authoritative %v\n%s", n.id, n.observed[h], h, owners, c.diagnosticsLocked())
		}
	}
}

// Calls returns how many invocations of h each node executed.
func (c *TestCluster) Calls(h HandlerID) map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.calls[h]))
	for id, count := range c.calls[h] {
		out[id] = count
	}
	return out
}

// Diagnostics renders nodes, placements, and recent events.
func (c *TestCluster) Diagnostics() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diagnosticsLocked()
}

func (c *TestCluster) diagnosticsLocked() string {
	var b strings.Builder
	fmt.Fprintf(&b, "TestCluster state at %s\n  nodes:\n", c.clock.Now().Format(time.RFC3339))
	for _, n := range c.nodes {
		handlers := make([]string, 0, len(n.regs))
		for _, r := range n.regs {
			handlers = append(handlers, r.id.String())
		}
		fmt.Fprintf(&b, "    %s state=%s gen=%d handlers=%v observed=%s\n", n.id, n.state, n.generation, handlers, formatPlacements(n.observed))
	}
	fmt.Fprintf(&b, "  placements: %s\n  leases:", formatPlacements(c.store))
	for _, capability := range sortedKeys(c.leases) {
		l := c.leases[capability].Lease
		fmt.Fprintf(&b, " %s=%s@%d/%d", capability, l.Holder, l.Epoch, l.Beat)
	}
	fmt.Fprintf(&b, "\n  calls: %v\n  events:\n", c.calls)
	start := 0
	if len(c.events) > 20 {
		start = len(c.events) - 20
	}
	for _, e := range c.events[start:] {
		fmt.Fprintf(&b, "    %s\n", e)
	}
	return b.String()
}

func (c *TestCluster) deliver(ctx context.Context, from, to *TestNode, request grove.RequestEnvelope) (grove.ResponseEnvelope, error) {
	c.mu.Lock()
	reachable := to.state == NodeRunning && from.state == NodeRunning && !to.isolated && !from.isolated &&
		!c.partitions[edge(from.id, to.id)]
	dispatcher := to.dispatcher
	if reachable {
		id := HandlerID{Service: request.ServiceID, Method: request.MethodID}
		if c.calls[id] == nil {
			c.calls[id] = make(map[string]int)
		}
		c.calls[id][to.id]++
	}
	c.mu.Unlock()
	if !reachable {
		return grove.ResponseEnvelope{}, fmt.Errorf("%s -> %s: %w", from.id, to.id, ErrNodeUnreachable)
	}
	return dispatcher.Dispatch(grove.WithExclusiveProvider(ctx, to), request), nil
}

// Epoch returns the fencing epoch of an exclusive handler; it increases every
// time ownership moves.
func (c *TestCluster) Epoch(h HandlerID) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.records[h].Epoch
}

// ownershipLocked is h's placement as the lease policy sees it.
func (c *TestCluster) ownershipLocked(h HandlerID) placement.Ownership {
	return c.records[h].Ownership()
}

// renewLeasesLocked renews every active lease of a reachable running node, as
// production renews on each reconcile tick: a holder whose placement moved, or
// whose stored lease another holder replaced, loses it instead.
func (c *TestCluster) renewLeasesLocked() {
	now := c.clock.Now()
	for _, n := range c.nodes {
		if n.state != NodeRunning || n.isolated {
			continue
		}
		for _, capability := range sortedKeys(n.held) {
			held := n.held[capability]
			if !held.Active() {
				continue
			}
			stored := c.leases[capability].Lease
			if !c.ownershipLocked(held.Handler).OwnedBy(n.id, held.Epoch) ||
				stored.Holder != n.id || stored.Epoch != held.Epoch || stored.Beat != held.Beat {
				held.Lost = true
				continue
			}
			renewal := held.Renewal(n.id)
			c.leases[capability] = placement.ObservedLease{Lease: renewal, FirstSeen: now}
			held.Beat, held.RenewedAt = renewal.Beat, now
		}
	}
}

// AcquireExclusive implements grove.ExclusiveProvider for handlers running on
// n with production's fencing policy, placement.DecideClaim: only the placed
// owner may claim, at its placement's epoch, and only once a previous
// holder's lease has gone unrenewed for the lease TTL. Where production would
// block until then, the test clock advances to that moment while reachable
// nodes keep renewing. A node that is not the owner gets an unheld lease.
func (n *TestNode) AcquireExclusive(_ context.Context, capability string) (grove.Lease, error) {
	c := n.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	var owned *registration
	for i, r := range n.regs {
		if r.info.Exclusive && capabilityName(r) == capability {
			owned = &n.regs[i]
		}
	}
	if owned == nil || n.state != NodeRunning || n.isolated {
		return deniedLease{}, nil
	}
	for range maxConvergeRounds {
		observed, seen := c.leases[capability]
		request := placement.ClaimRequest{
			NodeID:   n.id,
			Owner:    c.ownershipLocked(owned.id),
			Placed:   len(c.records[owned.id].Nodes) != 0,
			Held:     n.held[capability],
			Observed: observed,
			Seen:     seen,
			Now:      c.clock.Now(),
			TTL:      c.leaseTTL,
		}
		claim := placement.DecideClaim(request)
		switch claim.Decision {
		case placement.ClaimDenied:
			return deniedLease{}, nil
		case placement.ClaimHeld:
			return &testLease{node: n, held: n.held[capability], generation: n.generation}, nil
		case placement.ClaimWait:
			c.clock.Advance(placement.ClaimWaitUntil(request).Sub(c.clock.Now()))
			c.renewLeasesLocked()
			c.logf("%s waited for %s's lease on %q to expire", n.id, observed.Lease.Holder, capability)
			continue
		}
		now := c.clock.Now()
		c.leases[capability] = placement.ObservedLease{Lease: claim.Lease, FirstSeen: now}
		held := &placement.HeldLease{
			Capability: capability, Handler: owned.id, Epoch: claim.Lease.Epoch,
			Beat: claim.Lease.Beat, RenewedAt: now,
		}
		n.held[capability] = held
		c.logf("%s claimed %q at epoch %d", n.id, capability, held.Epoch)
		return &testLease{node: n, held: held, generation: n.generation}, nil
	}
	c.tb.Fatalf("%s: claim on %q did not settle\n%s", n.id, capability, c.diagnosticsLocked())
	return deniedLease{}, nil
}

func capabilityName(r registration) string {
	if r.info.Capability != "" {
		return r.info.Capability
	}
	return r.id.String()
}

type deniedLease struct{}

func (deniedLease) Held() bool { return false }
func (deniedLease) Release()   {}

// testLease is a claim held by one node process. It ends with that process
// (a crash or restart), and otherwise holds exactly as long as production's
// placement.LeaseHolds says.
type testLease struct {
	node       *TestNode
	held       *placement.HeldLease
	generation int
}

// Held reports whether the owner process is still running and the lease
// still holds under the production policy.
func (l *testLease) Held() bool {
	n, c := l.node, l.node.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	if n.state != NodeRunning || n.generation != l.generation {
		return false
	}
	return placement.LeaseHolds(*l.held, c.ownershipLocked(l.held.Handler), n.id, c.clock.Now(), c.leaseTTL)
}

func (l *testLease) Release() {
	l.node.cluster.mu.Lock()
	l.held.Released = true
	l.node.cluster.mu.Unlock()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestNode is one logical Grove node.
type TestNode struct {
	cluster *TestCluster
	id      string

	state      NodeState
	generation int
	lastSeen   time.Time // last heartbeat, once the node went silent
	leaving    bool
	detected   bool
	isolated   bool
	held       map[string]*placement.HeldLease // this process's claims by capability
	regs       []registration
	registry   *grove.Registry
	dispatcher *grove.Dispatcher
	client     *grove.Client
	observed   map[HandlerID][]string
	selector   placement.Selector
}

// ID returns the node's stable logical identity.
func (n *TestNode) ID() string { return n.id }

// State returns the node's lifecycle state.
func (n *TestNode) State() NodeState {
	n.cluster.mu.Lock()
	defer n.cluster.mu.Unlock()
	return n.state
}

// Client returns the node's routed Grove client; use it with grove.Call.
func (n *TestNode) Client() *grove.Client { return n.client }

// Observed returns the node's own placement view of h.
func (n *TestNode) Observed(h HandlerID) []string {
	n.cluster.mu.Lock()
	defer n.cluster.mu.Unlock()
	return append([]string(nil), n.observed[h]...)
}

// Register registers handler in the node's real Registry. Registrations
// survive RestartNode, as a restarted binary registers them again.
func (n *TestNode) Register(id HandlerID, handler grove.Handler, opts ...HandlerOption) {
	n.cluster.tb.Helper()
	n.cluster.mu.Lock()
	defer n.cluster.mu.Unlock()
	reg := registration{id: id, handler: handler}
	for _, opt := range opts {
		opt(&reg.info)
	}
	if err := n.registry.Register(id.Service, id.Method, handler); err != nil {
		n.cluster.tb.Fatalf("%s: %v", n.id, err)
	}
	n.regs = append(n.regs, reg)
}

// start (re)creates all local runtime state. The cluster lock must be held.
func (n *TestNode) start() {
	n.generation++
	n.state = NodeRunning
	n.detected = false
	n.leaving = false
	n.observed = nil
	n.selector = placement.Selector{}
	n.isolated = false
	n.held = make(map[string]*placement.HeldLease)
	n.registry = &grove.Registry{}
	for _, r := range n.regs {
		if err := n.registry.Register(r.id.Service, r.id.Method, r.handler); err != nil {
			n.cluster.tb.Fatalf("%s: %v", n.id, err)
		}
	}
	dispatcher, err := grove.NewDispatcher(n.registry)
	if err != nil {
		n.cluster.tb.Fatalf("%s: %v", n.id, err)
	}
	n.dispatcher = dispatcher
	n.cluster.logf("%s started (generation %d)", n.id, n.generation)
}

// nodeRouter is the node's grove.Router: it resolves a destination from the
// node's observed placements, then sends over the in-memory transport.
type nodeRouter struct{ node *TestNode }

func (r nodeRouter) Route(ctx context.Context, request grove.RequestEnvelope) (grove.ResponseEnvelope, error) {
	n, c := r.node, r.node.cluster
	id := HandlerID{Service: request.ServiceID, Method: request.MethodID}
	c.mu.Lock()
	if n.state != NodeRunning {
		c.mu.Unlock()
		return grove.ResponseEnvelope{}, fmt.Errorf("%s: %w", n.id, ErrNodeUnreachable)
	}
	targets := n.observed[id]
	if len(targets) == 0 {
		c.mu.Unlock()
		return grove.ResponseEnvelope{}, fmt.Errorf("%s: handler %s: %w", n.id, id, ErrHandlerNotPlaced)
	}
	targetID, _ := n.selector.Pick(id, targets)
	var target *TestNode
	for _, candidate := range c.nodes {
		if candidate.id == targetID {
			target = candidate
		}
	}
	c.mu.Unlock()
	return c.deliver(ctx, n, target, request)
}

func clonePlacements(in map[HandlerID][]string) map[HandlerID][]string {
	out := make(map[HandlerID][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func equalPlacements(a, b map[HandlerID][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !equalStrings(v, b[k]) {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatPlacements(p map[HandlerID][]string) string {
	keys := make([]HandlerID, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Service != keys[j].Service {
			return keys[i].Service < keys[j].Service
		}
		return keys[i].Method < keys[j].Method
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, p[k])
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// LeaseTTL returns how long an exclusive lease outlives its last renewal.
func (c *TestCluster) LeaseTTL() time.Duration { return c.leaseTTL }

// reachable reports whether the node runs and can talk to the cluster.
func (n *TestNode) reachable() bool { return n.state == NodeRunning && !n.isolated }
