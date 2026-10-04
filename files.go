package grove

import (
	"context"
	"errors"
	"time"
)

// Grove Files are cluster-managed local files. An application reads and
// writes an ordinary file at LocalPath with any library, decides when the
// file is consistent, and calls Sync to publish that state as an immutable,
// checksummed, replicated version. Grove does not replicate writes as they
// happen and is not a distributed filesystem.

var (
	// ErrFilesUnavailable is returned when no Grove runtime provides files to
	// the context.
	ErrFilesUnavailable = errors.New("grove files are unavailable in this context")
	// ErrFileOwned is returned when another node holds write ownership of a
	// file.
	ErrFileOwned = errors.New("file is owned by another node")
	// ErrOwnershipLost is returned when an owned file's ownership moved to
	// another node or could not be renewed. The handle can no longer publish.
	ErrOwnershipLost = errors.New("file ownership lost")
	// ErrFileChanged is returned when an opened, unowned file is synced after
	// another node committed a newer version than the one it was opened at.
	ErrFileChanged = errors.New("file changed since it was opened")
	// ErrDurability is returned when Sync could not reach the configured
	// durability. The previous committed version stays current.
	ErrDurability = errors.New("durability not satisfied")
	// ErrInvalidPath is returned for a logical path that is empty, absolute
	// or escapes the Grove-managed root.
	ErrInvalidPath = errors.New("invalid grove file path")
	// ErrFileClosed is returned when a closed or released handle is used.
	ErrFileClosed = errors.New("file is closed")
)

// Durability is the acknowledgement boundary of Sync: Sync returns only
// after a version is committed, and a version is committed only once the
// mode's condition holds.
type Durability int

const (
	// Replicated commits a version once every configured replica, the
	// writer's own copy included, has persisted and verified it.
	Replicated Durability = iota
	// Quorum commits a version once a majority of the configured replicas
	// has persisted and verified it.
	Quorum
	// Local commits a version once the writer has persisted and verified it.
	// Losing the writer's disk can lose the latest version.
	Local
)

func (d Durability) String() string {
	switch d {
	case Replicated:
		return "replicated"
	case Quorum:
		return "quorum"
	case Local:
		return "local"
	}
	return "unknown"
}

// FileOptions configure a file. The options of the first Sync that creates a
// file are kept; later handles use the stored options.
type FileOptions struct {
	// Replicas is how many nodes keep each committed version, the writer
	// included. Zero means DefaultReplicas.
	Replicas int
	// Durability is when Sync acknowledges a version.
	Durability Durability
}

// DefaultReplicas is the replica count of a file created without Replicas.
const DefaultReplicas = 3

// FileOption sets one FileOptions field.
type FileOption func(*FileOptions)

// Replicas sets how many nodes keep each committed version, the writer
// included.
func Replicas(n int) FileOption { return func(o *FileOptions) { o.Replicas = n } }

// WithDurability sets when Sync acknowledges a version.
func WithDurability(d Durability) FileOption { return func(o *FileOptions) { o.Durability = d } }

// ApplyFileOptions returns the options opts set over the defaults.
func ApplyFileOptions(opts ...FileOption) FileOptions {
	options := FileOptions{Replicas: DefaultReplicas, Durability: Replicated}
	for _, opt := range opts {
		opt(&options)
	}
	if options.Replicas <= 0 {
		options.Replicas = DefaultReplicas
	}
	return options
}

// Version identifies one immutable committed state of a file. Generations
// increase with every commit, so versions of one file are totally ordered.
type Version struct {
	ID         string
	Generation uint64
	Size       int64
	// SHA256 is the hex-encoded checksum of the version's bytes.
	SHA256    string
	CreatedAt time.Time
	// Source is the node that synced the version.
	Source string
}

// IsZero reports whether v names no version, as for a file never synced.
func (v Version) IsZero() bool { return v.ID == "" }

// File is a logical Grove file materialized at a local path.
type File interface {
	// Path is the logical, cluster-wide path.
	Path() string
	// LocalPath is the node-local file the application reads and writes.
	// Libraries open it as an ordinary file.
	LocalPath() string
	// CurrentVersion is the latest committed version this handle knows.
	CurrentVersion() Version
	// Sync publishes the bytes at LocalPath as a new committed version. Call
	// it only when the file is in a consistent state; Grove snapshots the
	// file before it replicates it. Sync returns after the version satisfies
	// the file's durability, or fails and leaves the previous version current.
	Sync(ctx context.Context) (Version, error)
	// WaitReplicated waits until every configured replica holds v. It
	// returns nil early when a newer version supersedes v.
	WaitReplicated(ctx context.Context, v Version) error
	// Close ends the handle. The local file stays where it is.
	Close() error
}

// OwnedFile is a file this node holds exclusive write ownership of. Ownership
// is fenced: once it moves to another node, Sync fails with ErrOwnershipLost.
type OwnedFile interface {
	File
	// Owner is the node that holds ownership.
	Owner() string
	// Held reports whether this handle still owns the file.
	Held() bool
	// Release gives up ownership.
	Release(ctx context.Context) error
}

// FileStore opens and acquires Grove files. Runtime packages implement it and
// attach it to handler contexts with WithFileStore.
type FileStore interface {
	// Open materializes the latest committed version of path locally. Sync
	// through an opened file succeeds only while no other node committed a
	// newer version and no node owns the file.
	Open(ctx context.Context, path string, opts ...FileOption) (File, error)
	// Acquire takes exclusive write ownership of path, waiting until a
	// previous owner's ownership has ended or ctx ends, and materializes the
	// latest committed version locally.
	Acquire(ctx context.Context, path string, opts ...FileOption) (OwnedFile, error)
}

type fileStoreKey struct{}

// WithFileStore returns ctx carrying store for Files.
func WithFileStore(ctx context.Context, store FileStore) context.Context {
	return context.WithValue(ctx, fileStoreKey{}, store)
}

// Files returns the file store of the Grove runtime that serves ctx. Without
// one, every operation fails with ErrFilesUnavailable; tests attach a store
// with WithFileStore.
func Files(ctx context.Context) FileStore {
	if store, ok := ctx.Value(fileStoreKey{}).(FileStore); ok && store != nil {
		return store
	}
	return unavailableFiles{}
}

type unavailableFiles struct{}

func (unavailableFiles) Open(context.Context, string, ...FileOption) (File, error) {
	return nil, ErrFilesUnavailable
}

func (unavailableFiles) Acquire(context.Context, string, ...FileOption) (OwnedFile, error) {
	return nil, ErrFilesUnavailable
}
