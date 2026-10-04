package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove/internal/files"
	"github.com/grove-project/grove/internal/systemnats"
)

// startFiles serves Grove Files for this Grovlet: the node-local file store
// under the runtime directory, the replica endpoints other nodes use, and the
// local endpoint this node's application processes use. Liveness comes from
// the same health view handler placement uses.
func (g *grovlet) startFiles(ctx context.Context, cfg config, health *systemnats.Health) error {
	catalog := g.transport.FilesCatalog().FollowMembership(g.membership)
	node, err := files.NewNode(files.Config{
		NodeID:  cfg.nodeID,
		Dir:     filepath.Join(cfg.runtimeDir, "files"),
		Catalog: catalog,
		Peers:   g.transport.FilesPeers(),
		Live:    liveNodesFromHealth(health),
		Events: func(event files.Event) {
			if cfg.emit == nil {
				return
			}
			detail := strings.TrimSpace(fmt.Sprintf("%s %s %s %s", event.Path, event.Version, event.Peer, event.Err))
			cfg.emit(lifecycleEvent{
				Event:  "file_" + strings.ReplaceAll(string(event.Kind), ".", "_"),
				NodeID: cfg.nodeID,
				Detail: detail,
			})
		},
	})
	if err != nil {
		return fmt.Errorf("start grove files: %w", err)
	}
	if err := g.transport.ServeFiles(ctx, cfg.nodeID, node); err != nil {
		return err
	}
	if err := g.transport.ServeLocalFiles(ctx, cfg.nodeID, node, systemnats.DefaultLocalFileTTL); err != nil {
		return err
	}
	g.filesLoop = goBackground(ctx, func(ctx context.Context) {
		var group sync.WaitGroup
		group.Go(func() { _ = node.Run(ctx) })
		group.Go(func() { reconcileFilesCatalog(ctx, cfg, catalog) })
		group.Wait()
	})
	return nil
}

// reconcileFilesCatalog grows the catalog bucket with membership. The bucket
// appears on the first file write, sized for the members then.
func reconcileFilesCatalog(ctx context.Context, cfg config, catalog *systemnats.FilesCatalog) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	reported := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := catalog.ReconcileReplicas(ctx)
		if err != nil && err.Error() != reported && cfg.emit != nil {
			cfg.emit(lifecycleEvent{Event: "file_catalog_replicas_failed", NodeID: cfg.nodeID, Detail: err.Error()})
		}
		reported = ""
		if err != nil {
			reported = err.Error()
		}
	}
}
