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

// filesService is the Grovlet's Grove Files service: the node-local file
// store under the runtime directory, the replica endpoints other nodes use,
// and the local endpoint this node's application processes use.
type filesService struct {
	node   *files.Node
	cancel context.CancelFunc
	done   chan struct{}
}

// startFiles serves Grove Files for this Grovlet. Liveness comes from the
// same health view handler placement uses.
func (r *systemNATSRuntime) startFiles(ctx context.Context, cfg config, health *systemnats.Health) error {
	catalog := r.transport.FilesCatalog().FollowMembership(r.membership)
	node, err := files.NewNode(files.Config{
		NodeID:  cfg.nodeID,
		Dir:     filepath.Join(cfg.runtimeDir, "files"),
		Catalog: catalog,
		Peers:   r.transport.FilesPeers(),
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
	filesCtx, cancel := context.WithCancel(ctx)
	if err := r.transport.ServeFiles(filesCtx, cfg.nodeID, node); err != nil {
		cancel()
		return err
	}
	if err := r.transport.ServeLocalFiles(filesCtx, cfg.nodeID, node, systemnats.DefaultLocalFileTTL); err != nil {
		cancel()
		return err
	}
	service := &filesService{node: node, cancel: cancel, done: make(chan struct{})}
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		_ = node.Run(filesCtx)
	}()
	go func() {
		defer group.Done()
		// The catalog bucket appears on the first file write, sized for the
		// members then; grow it as more join.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		reported := ""
		for {
			select {
			case <-filesCtx.Done():
				return
			case <-ticker.C:
			}
			err := catalog.ReconcileReplicas(filesCtx)
			if err != nil && err.Error() != reported && cfg.emit != nil {
				cfg.emit(lifecycleEvent{Event: "file_catalog_replicas_failed", NodeID: cfg.nodeID, Detail: err.Error()})
			}
			reported = ""
			if err != nil {
				reported = err.Error()
			}
		}
	}()
	go func() {
		group.Wait()
		close(service.done)
	}()
	r.files = service
	return nil
}

func (r *systemNATSRuntime) stopFiles() {
	if r.files != nil {
		r.files.cancel()
		<-r.files.done
		r.files = nil
	}
}
