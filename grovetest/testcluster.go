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
type HandlerID struct {
	Service grove.ServiceID
	Method  grove.MethodID
}

func (h HandlerID) String() string { return fmt.Sprintf("%d.%d", h.Service, h.Method) }

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

// EverywherePolicy places every handler on every live node that registered it
// and every exclusive handler on exactly one of them, sticking with the
// current owner while it remains eligible. It is the default until the
// production placer is wired in via WithPlacementPolicy.
type EverywherePolicy struct{}

// Place implements PlacementPolicy.
func (EverywherePolicy) Place(t Topology) map[HandlerID][]string {
	eligible := make(map[HandlerID][]string)
	exclusive := make(map[HandlerID]bool)
	for _, node := range t.Nodes {
		for id, info := range node.Handlers {
			eligible[id] = append(eligible[id], node.ID)
			exclusive[id] = exclusive[id] || info.Exclusive
		}
	}
	placements := make(map[HandlerID][]string, len(eligible))
	for id, ids := range eligible {
		sort.Strings(ids)
		if !exclusive[id] {
			placements[id] = ids
			continue
		}
		owner := ids[0]
		for _, current := range t.Current[id] {
			for _, candidate := range ids {
				if candidate == current {
					owner = current
				}
			}
		}
		placements[id] = []string{owner}
	}
	return placements
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

type registration struct {
	id      HandlerID
	handler grove.Handler
	info    HandlerInfo
}

// TestCluster runs several logical Grove nodes in one goroutine-free,
// deterministic in-process cluster. Each node owns a real grove.Registry,
// grove.Dispatcher, and routed grove.Client; only the network, node
// processes, and clock are simulated. Methods are for use by one test
// goroutine, though registered handlers may call back through node clients.
type TestCluster struct {
	tb          testing.TB
	clock       *Clock
	policy      PlacementPolicy
	detectAfter time.Duration

	mu         sync.Mutex
	nodes      []*TestNode
	store      map[HandlerID][]string // authoritative placements
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
		partitions:  make(map[[2]string]bool),
		calls:       make(map[HandlerID]map[string]int),
	}
	for _, opt := range opts {
		opt(c)
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
	n.observed = nil
	c.logf("%s stopped gracefully", n.id)
}

// KillNode makes n vanish abruptly. Peers keep their stale placements until
// failure detection elapses during Converge.
func (c *TestCluster) KillNode(n *TestNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n.state = NodeCrashed
	n.crashedAt = c.clock.Now()
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

func (c *TestCluster) detectFailures() {
	for _, n := range c.nodes {
		if n.state != NodeCrashed || n.detected {
			continue
		}
		if wait := n.crashedAt.Add(c.detectAfter).Sub(c.clock.Now()); wait > 0 {
			c.clock.Advance(wait)
		}
		n.detected = true
		n.observed = nil
		c.logf("%s failure detected", n.id)
	}
}

// reconcileLocked reports whether it changed any published or observed state.
func (c *TestCluster) reconcileLocked() bool {
	topology := Topology{Current: clonePlacements(c.store)}
	for _, n := range c.nodes {
		if n.state != NodeRunning {
			continue
		}
		info := NodeInfo{ID: n.id, Handlers: make(map[HandlerID]HandlerInfo, len(n.regs))}
		for _, r := range n.regs {
			info.Handlers[r.id] = r.info
		}
		topology.Nodes = append(topology.Nodes, info)
	}
	desired := c.policy.Place(topology)
	changed := !equalPlacements(c.store, desired)
	if changed {
		c.store = clonePlacements(desired)
		c.logf("placements published: %s", formatPlacements(c.store))
	}
	for _, n := range c.nodes {
		if n.state == NodeRunning && !equalPlacements(n.observed, c.store) {
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
		if n.state == NodeRunning && !equalStrings(n.observed[h], owners) {
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
	fmt.Fprintf(&b, "  placements: %s\n  calls: %v\n  events:\n", formatPlacements(c.store), c.calls)
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
	reachable := to.state == NodeRunning && from.state == NodeRunning && !c.partitions[edge(from.id, to.id)]
	dispatcher := to.dispatcher
	if reachable {
		id := HandlerID{request.ServiceID, request.MethodID}
		if c.calls[id] == nil {
			c.calls[id] = make(map[string]int)
		}
		c.calls[id][to.id]++
	}
	c.mu.Unlock()
	if !reachable {
		return grove.ResponseEnvelope{}, fmt.Errorf("%s -> %s: %w", from.id, to.id, ErrNodeUnreachable)
	}
	return dispatcher.Dispatch(ctx, request), nil
}

// TestNode is one logical Grove node.
type TestNode struct {
	cluster *TestCluster
	id      string

	state      NodeState
	generation int
	crashedAt  time.Time
	detected   bool
	regs       []registration
	registry   *grove.Registry
	dispatcher *grove.Dispatcher
	client     *grove.Client
	observed   map[HandlerID][]string
	next       map[HandlerID]int
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
	n.observed = nil
	n.next = make(map[HandlerID]int)
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
	id := HandlerID{request.ServiceID, request.MethodID}
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
	targetID := targets[n.next[id]%len(targets)]
	n.next[id]++
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
