package files

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrUnreachable is returned by Network for a node that is down or cut off.
var ErrUnreachable = errors.New("node unreachable")

// Network connects in-process nodes as Peers. Nodes can be taken down or cut
// off from each other to simulate failures and partitions.
type Network struct {
	mu    sync.Mutex
	nodes map[string]*Node
	down  map[string]bool
}

// NewNetwork returns an empty network.
func NewNetwork() *Network {
	return &Network{nodes: make(map[string]*Node), down: make(map[string]bool)}
}

// Add connects node, replacing a previous node with the same identity.
func (w *Network) Add(node *Node) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nodes[node.ID()] = node
	delete(w.down, node.ID())
}

// SetDown makes node unreachable, or reachable again.
func (w *Network) SetDown(node string, down bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.down[node] = down
}

// Live lists the reachable nodes.
func (w *Network) Live() ([]string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var live []string
	for id := range w.nodes {
		if !w.down[id] {
			live = append(live, id)
		}
	}
	return live, true
}

// Peers returns the network as seen from node from.
func (w *Network) Peers(from string) Peers { return networkPeers{network: w, from: from} }

func (w *Network) reach(from, to string) (*Node, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	node, ok := w.nodes[to]
	if !ok || w.down[to] || w.down[from] {
		return nil, fmt.Errorf("%w: %s", ErrUnreachable, to)
	}
	return node, nil
}

type networkPeers struct {
	network *Network
	from    string
}

func (p networkPeers) Replicate(ctx context.Context, node string, request ReplicateRequest) error {
	target, err := p.network.reach(p.from, node)
	if err != nil {
		return err
	}
	return target.HandleReplicate(ctx, request)
}

func (p networkPeers) ReadBlob(_ context.Context, node string, request BlobRequest) ([]byte, error) {
	target, err := p.network.reach(p.from, node)
	if err != nil {
		return nil, err
	}
	return target.HandleReadBlob(request)
}
