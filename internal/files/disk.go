package files

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/grove-project/grove"
)

// Disk is one node's durable Grove Files state. Its layout keeps writable
// working files, partial transfers and verified committed versions apart:
//
//	<root>/catalog/cluster.json         cluster lineage of every local file
//	<root>/objects/<file-id>/file.json  logical path and options
//	<root>/objects/<file-id>/versions/  verified immutable <version>.blob and .meta
//	<root>/objects/<file-id>/current    newest version this node knows is committed
//	<root>/objects/<file-id>/work/      the writable local materialization
//	<root>/objects/<file-id>/staging/   snapshots and transfers in progress
//	<root>/quarantine/                  corrupt or foreign state, never read again
//
// Every persisted file is written to a temporary path, synced, and renamed
// into place, so a crash leaves either the old or the new content.
type Disk struct {
	root string
}

// LocalVersion is the metadata stored beside a verified version blob.
type LocalVersion struct {
	Format    int         `json:"format"`
	ClusterID string      `json:"cluster_id"`
	Path      string      `json:"path"`
	FileID    string      `json:"file_id"`
	Version   VersionMeta `json:"version"`
}

// LocalFile is the identity and options of one file this node stores.
type LocalFile struct {
	Format     int              `json:"format"`
	Path       string           `json:"path"`
	FileID     string           `json:"file_id"`
	Replicas   int              `json:"desired_replicas"`
	Durability grove.Durability `json:"durability_mode"`
}

type workState struct {
	Format int    `json:"format"`
	Base   string `json:"base_version,omitempty"`
}

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// OpenDisk prepares root and removes transfers and snapshots a previous
// process left unfinished.
func OpenDisk(root string) (*Disk, error) {
	if root == "" {
		return nil, errors.New("grove files directory is required")
	}
	d := &Disk{root: root}
	for _, dir := range []string{d.catalogDir(), d.objectsDir(), d.quarantineDir()} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return nil, fmt.Errorf("prepare grove files directory: %w", err)
		}
	}
	ids, err := d.FileIDs()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := os.RemoveAll(d.stagingDir(id)); err != nil {
			return nil, fmt.Errorf("remove abandoned staging: %w", err)
		}
	}
	return d, nil
}

func (d *Disk) catalogDir() string           { return filepath.Join(d.root, "catalog") }
func (d *Disk) objectsDir() string           { return filepath.Join(d.root, "objects") }
func (d *Disk) quarantineDir() string        { return filepath.Join(d.root, "quarantine") }
func (d *Disk) fileDir(id string) string     { return filepath.Join(d.objectsDir(), id) }
func (d *Disk) versionsDir(id string) string { return filepath.Join(d.fileDir(id), "versions") }
func (d *Disk) stagingDir(id string) string  { return filepath.Join(d.fileDir(id), "staging") }
func (d *Disk) workDir(id string) string     { return filepath.Join(d.fileDir(id), "work") }

func (d *Disk) blobPath(id, version string) string {
	return filepath.Join(d.versionsDir(id), version+".blob")
}

func (d *Disk) metaPath(id, version string) string {
	return filepath.Join(d.versionsDir(id), version+".meta")
}

// ClusterID returns the cluster lineage stored on this disk, or "".
func (d *Disk) ClusterID() (string, error) {
	var record ClusterRecord
	ok, err := readJSON(filepath.Join(d.catalogDir(), "cluster.json"), &record)
	if err != nil || !ok {
		return "", err
	}
	return record.ClusterID, nil
}

// SetClusterID stores the cluster lineage of this disk.
func (d *Disk) SetClusterID(id string, now time.Time) error {
	return writeJSON(filepath.Join(d.catalogDir(), "cluster.json"),
		ClusterRecord{Format: FormatVersion, ClusterID: id, CreatedAt: now})
}

// FileIDs lists the files this disk stores.
func (d *Disk) FileIDs() ([]string, error) {
	entries, err := os.ReadDir(d.objectsDir())
	if err != nil {
		return nil, fmt.Errorf("list grove files: %w", err)
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return ids, nil
}

// File returns the stored identity of file id.
func (d *Disk) File(id string) (LocalFile, bool, error) {
	var file LocalFile
	ok, err := readJSON(filepath.Join(d.fileDir(id), "file.json"), &file)
	return file, ok, err
}

// PutFile stores the identity and options of a file.
func (d *Disk) PutFile(file LocalFile) error {
	file.Format = FormatVersion
	if err := os.MkdirAll(d.fileDir(file.FileID), dirMode); err != nil {
		return fmt.Errorf("prepare grove file: %w", err)
	}
	return writeJSON(filepath.Join(d.fileDir(file.FileID), "file.json"), file)
}

// Versions lists the verified versions of file id in generation order.
func (d *Disk) Versions(id string) ([]LocalVersion, error) {
	entries, err := os.ReadDir(d.versionsDir(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list grove file versions: %w", err)
	}
	var versions []LocalVersion
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".meta")
		if !ok {
			continue
		}
		version, found, err := d.Version(id, name)
		if err != nil {
			return nil, err
		}
		if found {
			versions = append(versions, version)
		}
	}
	slices.SortFunc(versions, func(a, b LocalVersion) int {
		return compareGenerations(a.Version.Generation, b.Version.Generation)
	})
	return versions, nil
}

// Version returns the stored metadata of one version of file id.
func (d *Disk) Version(id, version string) (LocalVersion, bool, error) {
	var local LocalVersion
	ok, err := readJSON(d.metaPath(id, version), &local)
	if err != nil || !ok {
		return LocalVersion{}, false, err
	}
	if _, err := os.Stat(d.blobPath(id, version)); err != nil {
		return LocalVersion{}, false, nil
	}
	return local, true, nil
}

// Verify rehashes a stored version and checks it against its metadata and,
// when want is set, against the checksum the cluster recorded.
func (d *Disk) Verify(id string, want VersionMeta) error {
	local, ok, err := d.Version(id, want.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("version %s: %w", want.ID, fs.ErrNotExist)
	}
	if local.Version.SHA256 != want.SHA256 || local.Version.Size != want.Size {
		return fmt.Errorf("%w: version %s metadata does not match the catalog", ErrIntegrity, want.ID)
	}
	size, sum, err := hashFile(d.blobPath(id, want.ID))
	if err != nil {
		return err
	}
	if size != want.Size || sum != want.SHA256 {
		return fmt.Errorf("%w: version %s has %d bytes sha256 %s; want %d bytes sha256 %s",
			ErrIntegrity, want.ID, size, sum, want.Size, want.SHA256)
	}
	return nil
}

// Current returns the newest version this node knows is committed.
func (d *Disk) Current(id string) (string, error) {
	data, err := os.ReadFile(filepath.Join(d.fileDir(id), "current"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read current grove file version: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// SetCurrent records version as the newest committed version on this node.
func (d *Disk) SetCurrent(id, version string) error {
	return writeFileAtomic(filepath.Join(d.fileDir(id), "current"), []byte(version+"\n"))
}

// ClearCurrent forgets the committed pointer, as after its blob was found
// corrupt.
func (d *Disk) ClearCurrent(id string) error {
	err := os.Remove(filepath.Join(d.fileDir(id), "current"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Snapshot copies the work file of id into an immutable staging file and
// returns it with its size and checksum. Replication reads only the
// snapshot, never the file the application keeps writing.
func (d *Disk) Snapshot(id, logical string) (string, int64, string, error) {
	if err := os.MkdirAll(d.stagingDir(id), dirMode); err != nil {
		return "", 0, "", fmt.Errorf("prepare grove file staging: %w", err)
	}
	source, err := os.Open(d.WorkPath(id, logical))
	if err != nil {
		return "", 0, "", fmt.Errorf("open grove file for sync: %w", err)
	}
	defer source.Close()
	staged, err := os.CreateTemp(d.stagingDir(id), "snapshot-*")
	if err != nil {
		return "", 0, "", fmt.Errorf("create grove file snapshot: %w", err)
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(staged, hash), source)
	if err == nil {
		err = staged.Sync()
	}
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(staged.Name())
		return "", 0, "", fmt.Errorf("snapshot grove file: %w", err)
	}
	return staged.Name(), size, hex.EncodeToString(hash.Sum(nil)), nil
}

// Receive writes a version's bytes, read in order from next until it
// returns io.EOF, into a staging file and installs it once its size and
// checksum match. Nothing becomes visible unless the bytes verify.
func (d *Disk) Receive(local LocalVersion, next func(offset int64) ([]byte, error)) error {
	id := local.FileID
	if err := os.MkdirAll(d.stagingDir(id), dirMode); err != nil {
		return fmt.Errorf("prepare grove file staging: %w", err)
	}
	staged, err := os.CreateTemp(d.stagingDir(id), "transfer-*")
	if err != nil {
		return fmt.Errorf("create grove file transfer: %w", err)
	}
	var offset int64
	for err == nil {
		var chunk []byte
		chunk, err = next(offset)
		if len(chunk) > 0 {
			if offset+int64(len(chunk)) > local.Version.Size {
				err = fmt.Errorf("%w: transfer exceeds %d bytes", ErrIntegrity, local.Version.Size)
				break
			}
			if _, writeErr := staged.Write(chunk); writeErr != nil {
				err = writeErr
				break
			}
			offset += int64(len(chunk))
		}
	}
	if errors.Is(err, io.EOF) {
		err = staged.Sync()
	}
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(staged.Name())
		return fmt.Errorf("receive grove file version %s: %w", local.Version.ID, err)
	}
	return d.Install(local, staged.Name())
}

// Install moves a staged file into the verified versions of its file once
// it matches the version's size and checksum. A mismatching file is
// removed and never becomes visible.
func (d *Disk) Install(local LocalVersion, staged string) error {
	id, version := local.FileID, local.Version
	size, sum, err := hashFile(staged)
	if err == nil && (size != version.Size || sum != version.SHA256) {
		err = fmt.Errorf("%w: received %d bytes sha256 %s; want %d bytes sha256 %s",
			ErrIntegrity, size, sum, version.Size, version.SHA256)
	}
	if err != nil {
		_ = os.Remove(staged)
		return err
	}
	if err := os.MkdirAll(d.versionsDir(id), dirMode); err != nil {
		return fmt.Errorf("prepare grove file versions: %w", err)
	}
	if err := os.Chmod(staged, 0o400); err != nil {
		return fmt.Errorf("protect grove file version: %w", err)
	}
	if err := os.Rename(staged, d.blobPath(id, version.ID)); err != nil {
		return fmt.Errorf("install grove file version: %w", err)
	}
	local.Format = FormatVersion
	// The metadata is written last: a blob without it is not a version.
	if err := writeJSON(d.metaPath(id, version.ID), local); err != nil {
		return err
	}
	return syncDir(d.versionsDir(id))
}

// ReadAt reads up to n bytes of a stored version from offset. It returns
// io.EOF once offset reaches the end.
func (d *Disk) ReadAt(id, version string, offset int64, n int) ([]byte, error) {
	if _, ok, err := d.Version(id, version); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("version %s: %w", version, fs.ErrNotExist)
		}
		return nil, err
	}
	blob, err := os.Open(d.blobPath(id, version))
	if err != nil {
		return nil, err
	}
	defer blob.Close()
	buffer := make([]byte, n)
	read, err := blob.ReadAt(buffer, offset)
	if errors.Is(err, io.EOF) && read > 0 {
		err = nil
	}
	return buffer[:read], err
}

// WorkPath is the local materialization of file id.
func (d *Disk) WorkPath(id, logical string) string {
	return filepath.Join(d.workDir(id), path.Base(logical))
}

// WorkBase returns the version the work file was materialized from, and
// whether a work file exists.
func (d *Disk) WorkBase(id, logical string) (string, bool, error) {
	if _, err := os.Stat(d.WorkPath(id, logical)); errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	var state workState
	if _, err := readJSON(filepath.Join(d.fileDir(id), "work.json"), &state); err != nil {
		return "", false, err
	}
	return state.Base, true, nil
}

// SetWorkBase records the version the work file now builds on.
func (d *Disk) SetWorkBase(id, version string) error {
	return writeJSON(filepath.Join(d.fileDir(id), "work.json"), workState{Format: FormatVersion, Base: version})
}

// Materialize replaces the work directory of id with a copy of version, or
// with an empty file when version is empty. The previous work directory,
// with local edits made on top of an older version and any side files the
// application kept next to the file (such as a database journal), is moved
// to quarantine rather than lost or applied to the new version.
func (d *Disk) Materialize(id, logical, version string) error {
	if err := d.quarantine(d.workDir(id), id+"-work"); err != nil {
		return err
	}
	if err := os.MkdirAll(d.workDir(id), dirMode); err != nil {
		return fmt.Errorf("prepare grove file work directory: %w", err)
	}
	work := d.WorkPath(id, logical)
	staged, err := os.CreateTemp(d.workDir(id), ".materialize-*")
	if err != nil {
		return fmt.Errorf("materialize grove file: %w", err)
	}
	if version != "" {
		blob, err := os.Open(d.blobPath(id, version))
		if err != nil {
			staged.Close()
			_ = os.Remove(staged.Name())
			return fmt.Errorf("materialize grove file: %w", err)
		}
		_, err = io.Copy(staged, blob)
		blob.Close()
		if err != nil {
			staged.Close()
			_ = os.Remove(staged.Name())
			return fmt.Errorf("materialize grove file: %w", err)
		}
	}
	if err := staged.Sync(); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Rename(staged.Name(), work); err != nil {
		return fmt.Errorf("materialize grove file: %w", err)
	}
	if err := syncDir(d.workDir(id)); err != nil {
		return err
	}
	return d.SetWorkBase(id, version)
}

// QuarantineVersion moves a version out of the verified set so it is never
// read, served or promoted again.
func (d *Disk) QuarantineVersion(id, version string) error {
	if current, err := d.Current(id); err == nil && current == version {
		if err := d.ClearCurrent(id); err != nil {
			return err
		}
	}
	// The metadata goes first so a crash never leaves a listed version
	// without its blob.
	if err := d.quarantine(d.metaPath(id, version), id+"-"+version+".meta"); err != nil {
		return err
	}
	return d.quarantine(d.blobPath(id, version), id+"-"+version+".blob")
}

// QuarantineAll moves every stored file aside, as when the disk belongs to
// another cluster lineage.
func (d *Disk) QuarantineAll() error {
	ids, err := d.FileIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := d.quarantine(d.fileDir(id), id); err != nil {
			return err
		}
	}
	return nil
}

// RemoveVersion deletes a superseded version.
func (d *Disk) RemoveVersion(id, version string) error {
	if err := os.Remove(d.metaPath(id, version)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(d.blobPath(id, version)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (d *Disk) quarantine(source, name string) error {
	if _, err := os.Stat(source); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	target := filepath.Join(d.quarantineDir(), fmt.Sprintf("%d-%s", time.Now().UnixNano(), name))
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("quarantine %s: %w", source, err)
	}
	return nil
}

// Quarantined lists the names in quarantine.
func (d *Disk) Quarantined() ([]string, error) {
	entries, err := os.ReadDir(d.quarantineDir())
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

func hashFile(name string) (int64, string, error) {
	file, err := os.Open(name)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func readJSON(name string, value any) (bool, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var header struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return false, fmt.Errorf("decode %s: %w", name, err)
	}
	if header.Format > FormatVersion {
		return false, fmt.Errorf("%w %d in %s", ErrFormat, header.Format, name)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return false, fmt.Errorf("decode %s: %w", name, err)
	}
	return true, nil
}

func writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(name, append(data, '\n'))
}

func writeFileAtomic(name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Chmod(fileMode)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), name)
	}
	if err != nil {
		_ = os.Remove(temp.Name())
		return fmt.Errorf("write %s: %w", name, err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func compareGenerations(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
