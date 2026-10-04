package systemnats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/files"
	"github.com/nats-io/nats.go"
)

// Application processes reach their Grovlet's file service through a local
// request/reply protocol. The Grovlet holds the handles and ownership; the
// application process gets the local path and calls Sync. A handle the
// application process stops renewing is closed, so a dead process cannot pin
// ownership.

const filesLocalRoot = "_GROVE.system.files.local."

type fileRequest struct {
	Op      string            `json:"op"` // open, acquire, sync, wait, renew, close, release
	Path    string            `json:"path,omitempty"`
	Options grove.FileOptions `json:"options,omitzero"`
	Token   string            `json:"token,omitempty"`
	Version grove.Version     `json:"version,omitzero"`
}

type fileReply struct {
	Status    string        `json:"status"` // ok, pending, error
	Code      string        `json:"code,omitempty"`
	Error     string        `json:"error,omitempty"`
	Token     string        `json:"token,omitempty"`
	Path      string        `json:"path,omitempty"`
	LocalPath string        `json:"local_path,omitempty"`
	Owner     string        `json:"owner,omitempty"`
	Held      bool          `json:"held,omitempty"`
	Version   grove.Version `json:"version,omitzero"`
}

var fileErrorCodes = []struct {
	code string
	err  error
}{
	{"owned", grove.ErrFileOwned},
	{"lost", grove.ErrOwnershipLost},
	{"changed", grove.ErrFileChanged},
	{"durability", grove.ErrDurability},
	{"invalid-path", grove.ErrInvalidPath},
	{"closed", grove.ErrFileClosed},
	{"unavailable", grove.ErrFilesUnavailable},
	{"no-replica", files.ErrNoEligibleReplica},
}

func fileErrorReply(err error) fileReply {
	reply := fileReply{Status: "error", Error: err.Error()}
	for _, known := range fileErrorCodes {
		if errors.Is(err, known.err) {
			reply.Code = known.code
			break
		}
	}
	return reply
}

func (r fileReply) err() error {
	if r.Status != "error" {
		return nil
	}
	for _, known := range fileErrorCodes {
		if r.Code == known.code {
			return fmt.Errorf("%w: %s", known.err, r.Error)
		}
	}
	return errors.New(r.Error)
}

type localFileHandle struct {
	handle   *files.Handle
	lastSeen time.Time
}

// ServeLocalFiles serves node's file service to the application processes
// on nodeID. Handles not renewed within ttl are closed.
func (t *Transport) ServeLocalFiles(ctx context.Context, nodeID string, node *files.Node, ttl time.Duration) error {
	var mu sync.Mutex
	handles := make(map[string]*localFileHandle)
	go func() {
		ticker := time.NewTicker(ttl / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			mu.Lock()
			for token, h := range handles {
				if time.Since(h.lastSeen) >= ttl {
					_ = h.handle.Close()
					delete(handles, token)
				}
			}
			mu.Unlock()
		}
	}()
	lookup := func(token string) (*files.Handle, bool) {
		mu.Lock()
		defer mu.Unlock()
		h, ok := handles[token]
		if ok {
			h.lastSeen = time.Now()
		}
		if !ok {
			return nil, false
		}
		return h.handle, true
	}
	register := func(handle *files.Handle) fileReply {
		raw := make([]byte, 16)
		_, _ = rand.Read(raw)
		token := hex.EncodeToString(raw)
		mu.Lock()
		handles[token] = &localFileHandle{handle: handle, lastSeen: time.Now()}
		mu.Unlock()
		reply := fileReply{
			Status: "ok", Token: token, Path: handle.Path(), LocalPath: handle.LocalPath(),
			Version: handle.CurrentVersion(),
		}
		if handle.Epoch() != 0 {
			reply.Owner, reply.Held = handle.Owner(), handle.Held()
		}
		return reply
	}
	serve := func(request fileRequest) fileReply {
		options := []grove.FileOption{grove.Replicas(request.Options.Replicas), grove.WithDurability(request.Options.Durability)}
		switch request.Op {
		case "open":
			handle, err := node.OpenHandle(ctx, request.Path, options...)
			if err != nil {
				return fileErrorReply(err)
			}
			return register(handle)
		case "acquire":
			handle, done, err := node.TryAcquire(ctx, request.Path, options...)
			switch {
			case err != nil:
				return fileErrorReply(err)
			case !done:
				return fileReply{Status: "pending"}
			}
			return register(handle)
		}
		handle, ok := lookup(request.Token)
		if !ok {
			return fileErrorReply(grove.ErrFileClosed)
		}
		switch request.Op {
		case "sync":
			version, err := handle.Sync(ctx)
			if err != nil {
				return fileErrorReply(err)
			}
			return fileReply{Status: "ok", Version: version}
		case "wait":
			waitCtx, cancel := context.WithTimeout(ctx, ttl)
			defer cancel()
			if err := handle.WaitReplicated(waitCtx, request.Version); err != nil {
				return fileReply{Status: "pending"}
			}
			return fileReply{Status: "ok"}
		case "renew":
			return fileReply{Status: "ok", Held: handle.Held(), Version: handle.CurrentVersion()}
		case "close", "release":
			mu.Lock()
			delete(handles, request.Token)
			mu.Unlock()
			if err := handle.Close(); err != nil {
				return fileErrorReply(err)
			}
			return fileReply{Status: "ok"}
		}
		return fileErrorReply(fmt.Errorf("unknown file operation %q", request.Op))
	}
	subscription, err := t.connection.Subscribe(filesLocalRoot+nodeID, func(message *nats.Msg) {
		go func() {
			var request fileRequest
			reply := fileErrorReply(errors.New("malformed file request"))
			if json.Unmarshal(message.Data, &request) == nil {
				reply = serve(request)
			}
			if encoded, err := json.Marshal(reply); err == nil {
				_ = message.Respond(encoded)
			}
		}()
	})
	if err != nil {
		return &Error{Operation: "subscribe Grove local files endpoint", Err: err}
	}
	go func() {
		<-ctx.Done()
		_ = subscription.Unsubscribe()
	}()
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove local files endpoint", Err: err}
	}
	return nil
}

// RemoteFileStore is the grove.FileStore of an application process: it
// reaches the file service of the Grovlet nodeID that hosts it.
type RemoteFileStore struct {
	transport *Transport
	nodeID    string
	ttl       time.Duration
	lifetime  context.Context
}

// NewRemoteFileStore returns a file store whose handle renewals run until
// lifetime ends. ttl must match the Grovlet's local handle TTL.
func (t *Transport) NewRemoteFileStore(lifetime context.Context, nodeID string, ttl time.Duration) *RemoteFileStore {
	if ttl <= 0 {
		ttl = DefaultLocalFileTTL
	}
	return &RemoteFileStore{transport: t, nodeID: nodeID, ttl: ttl, lifetime: lifetime}
}

// DefaultLocalFileTTL is how long the Grovlet keeps an application process's
// file handle without a renewal.
const DefaultLocalFileTTL = 3 * time.Second

func (s *RemoteFileStore) call(ctx context.Context, request fileRequest) (fileReply, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return fileReply{}, err
	}
	message, err := s.transport.connection.RequestWithContext(ctx, filesLocalRoot+s.nodeID, encoded)
	if errors.Is(err, nats.ErrNoResponders) {
		return fileReply{}, fmt.Errorf("%w: %w", grove.ErrFilesUnavailable, err)
	}
	if err != nil {
		return fileReply{}, fmt.Errorf("request grove file service: %w", err)
	}
	var reply fileReply
	if err := json.Unmarshal(message.Data, &reply); err != nil {
		return fileReply{}, err
	}
	return reply, reply.err()
}

// Open implements grove.FileStore.
func (s *RemoteFileStore) Open(ctx context.Context, path string, opts ...grove.FileOption) (grove.File, error) {
	reply, err := s.call(ctx, fileRequest{Op: "open", Path: path, Options: grove.ApplyFileOptions(opts...)})
	if err != nil {
		return nil, err
	}
	return s.start(reply, false), nil
}

// Acquire implements grove.FileStore.
func (s *RemoteFileStore) Acquire(ctx context.Context, path string, opts ...grove.FileOption) (grove.OwnedFile, error) {
	ticker := time.NewTicker(s.ttl / 6)
	defer ticker.Stop()
	for {
		reply, err := s.call(ctx, fileRequest{Op: "acquire", Path: path, Options: grove.ApplyFileOptions(opts...)})
		if err == nil && reply.Status == "ok" {
			return s.start(reply, true), nil
		}
		if err != nil && ctx.Err() == nil {
			return nil, err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %s: %w", grove.ErrFileOwned, path, ctx.Err())
		}
	}
}

func (s *RemoteFileStore) start(reply fileReply, owned bool) *remoteFile {
	ctx, cancel := context.WithCancel(s.lifetime)
	file := &remoteFile{
		store: s, token: reply.Token, path: reply.Path, local: reply.LocalPath, owner: reply.Owner,
		owned: owned, held: reply.Held, lastOK: time.Now(), version: reply.Version, stop: cancel,
	}
	go file.renew(ctx)
	return file
}

type remoteFile struct {
	store *RemoteFileStore
	token string
	path  string
	local string
	owner string
	owned bool
	stop  context.CancelFunc

	mu      sync.Mutex
	held    bool
	lastOK  time.Time
	version grove.Version
	closed  bool
}

func (f *remoteFile) renew(ctx context.Context) {
	ticker := time.NewTicker(f.store.ttl / 6)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		callCtx, cancel := context.WithTimeout(ctx, f.store.ttl/6)
		reply, err := f.store.call(callCtx, fileRequest{Op: "renew", Token: f.token})
		cancel()
		f.mu.Lock()
		switch {
		case err == nil:
			f.lastOK, f.held = time.Now(), reply.Held
		case errors.Is(err, grove.ErrFileClosed):
			f.held = false
		}
		f.mu.Unlock()
	}
}

func (f *remoteFile) Path() string      { return f.path }
func (f *remoteFile) LocalPath() string { return f.local }
func (f *remoteFile) Owner() string     { return f.owner }

func (f *remoteFile) CurrentVersion() grove.Version {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version
}

// Held fails safe: a process that cannot confirm ownership recently stops
// acting.
func (f *remoteFile) Held() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owned && !f.closed && f.held && time.Since(f.lastOK) < f.store.ttl/2
}

func (f *remoteFile) Sync(ctx context.Context) (grove.Version, error) {
	reply, err := f.store.call(ctx, fileRequest{Op: "sync", Token: f.token})
	if err != nil {
		return grove.Version{}, err
	}
	f.mu.Lock()
	f.version = reply.Version
	f.mu.Unlock()
	return reply.Version, nil
}

func (f *remoteFile) WaitReplicated(ctx context.Context, version grove.Version) error {
	for {
		reply, err := f.store.call(ctx, fileRequest{Op: "wait", Token: f.token, Version: version})
		if err != nil {
			return err
		}
		if reply.Status == "ok" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (f *remoteFile) Close() error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(f.store.lifetime), 5*time.Second)
	defer cancel()
	return f.Release(ctx)
}

func (f *remoteFile) Release(ctx context.Context) error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed, f.held = true, false
	f.mu.Unlock()
	f.stop()
	_, err := f.store.call(ctx, fileRequest{Op: "release", Token: f.token})
	if errors.Is(err, grove.ErrFileClosed) {
		return nil
	}
	return err
}
