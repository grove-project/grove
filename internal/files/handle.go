package files

import (
	"context"
	"sync"
	"time"

	"github.com/grove-project/grove"
)

// Handle is one opened or acquired file. It implements grove.File and, when
// acquired, grove.OwnedFile.
type Handle struct {
	node    *Node
	path    string
	id      string
	local   string
	held    *ownership
	options []grove.FileOption

	mu     sync.Mutex
	base   *VersionMeta
	closed bool
}

// Path implements grove.File.
func (h *Handle) Path() string { return h.path }

// LocalPath implements grove.File.
func (h *Handle) LocalPath() string { return h.local }

// CurrentVersion implements grove.File.
func (h *Handle) CurrentVersion() grove.Version { return h.Base() }

// Base is the committed version the handle's local file builds on.
func (h *Handle) Base() grove.Version {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.base == nil {
		return grove.Version{}
	}
	return h.base.Public()
}

func (h *Handle) setBase(version VersionMeta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.base = &version
}

// Sync implements grove.File.
func (h *Handle) Sync(ctx context.Context) (grove.Version, error) {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return grove.Version{}, grove.ErrFileClosed
	}
	version, err := h.node.sync(ctx, h)
	if err != nil {
		return grove.Version{}, err
	}
	return version.Public(), nil
}

// WaitReplicated implements grove.File.
func (h *Handle) WaitReplicated(ctx context.Context, version grove.Version) error {
	return h.node.waitReplicated(ctx, h.id, version)
}

// Close implements grove.File. Closing an owned file releases ownership.
func (h *Handle) Close() error {
	if h.held == nil {
		h.mu.Lock()
		h.closed = true
		h.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.Release(ctx)
}

// Owner implements grove.OwnedFile.
func (h *Handle) Owner() string { return h.node.cfg.NodeID }

// Epoch is the fencing epoch of the handle's ownership, or zero.
func (h *Handle) Epoch() uint64 {
	if h.held == nil {
		return 0
	}
	return h.held.lease.Epoch
}

// Held implements grove.OwnedFile.
func (h *Handle) Held() bool {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	return !closed && h.held != nil && h.node.holds(h.held)
}

// Release implements grove.OwnedFile.
func (h *Handle) Release(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()
	if h.held == nil {
		return nil
	}
	return h.node.release(ctx, h.id, h.held)
}
