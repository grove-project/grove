package artifact

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

const (
	// CompilerProtocolVersion is the schema-independent subprocess protocol
	// shared by the CLI and target artifact.
	CompilerProtocolVersion = 1
	configBundleVersion     = 1
	configRegionMagic       = "GRVCFG01"
	configRegionHeaderSize  = len(configRegionMagic) + 4
)

var (
	// ErrCompilationInvalid is returned when a target compiler response is
	// incomplete or incompatible.
	ErrCompilationInvalid = errors.New("target configuration compilation is invalid")
	// ErrConfigAlreadyEmbedded is returned when embedding targets an artifact
	// that already has immutable configuration.
	ErrConfigAlreadyEmbedded = errors.New("artifact already contains configuration")
	// ErrConfigOverflow is returned when a compressed config bundle cannot fit
	// in the artifact's reserved region.
	ErrConfigOverflow = errors.New("compiled configuration exceeds the artifact reservation")
	// ErrConfigCorrupt is returned when embedded configuration fails framing,
	// decompression, or integrity checks.
	ErrConfigCorrupt = errors.New("embedded configuration is corrupt")
	// ErrConfigIncompatible is returned when embedded configuration uses an
	// unsupported bundle version.
	ErrConfigIncompatible = errors.New("embedded configuration is incompatible")
)

// Compilation is the schema-independent product returned by the exact target
// binary that owns configuration semantics.
type Compilation struct {
	ProtocolVersion int               `json:"protocol_version"`
	Revision        string            `json:"revision"`
	Encoding        string            `json:"encoding"`
	Payload         []byte            `json:"payload"`
	CanonicalYAML   []byte            `json:"canonical_yaml"`
	Facts           map[string]string `json:"facts,omitempty"`
}

// CompilationFailure is a structured semantic error returned by a target
// configuration compiler.
type CompilationFailure struct {
	Kind    string `json:"kind"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// CompilationResponse is the versioned target-binary compiler response.
type CompilationResponse struct {
	ProtocolVersion int                 `json:"protocol_version"`
	Compilation     *Compilation        `json:"compilation,omitempty"`
	Failure         *CompilationFailure `json:"failure,omitempty"`
}

// ConfigMetadata describes verified embedded configuration without exposing
// application-owned values.
type ConfigMetadata struct {
	Revision       string
	Digest         string
	Encoding       string
	Compression    string
	CompiledSize   int
	CanonicalSize  int
	CompressedSize int
	Facts          map[string]string
}

// OverflowError reports the exact fixed-region capacity violation.
type OverflowError struct {
	Required int
	Capacity int
}

func (e *OverflowError) Error() string {
	return fmt.Sprintf("%v: requires %d bytes, capacity is %d bytes", ErrConfigOverflow, e.Required, e.Capacity)
}

func (e *OverflowError) Unwrap() error {
	return ErrConfigOverflow
}

type configBundle struct {
	FormatVersion int               `json:"format_version"`
	Revision      string            `json:"revision"`
	Encoding      string            `json:"encoding"`
	ConfigDigest  string            `json:"config_digest"`
	Payload       []byte            `json:"payload"`
	CanonicalYAML []byte            `json:"canonical_yaml"`
	Facts         map[string]string `json:"facts,omitempty"`
}

// ValidateCompilation validates only the generic compiler protocol. It does
// not inspect YAML or application-owned runtime bytes.
func ValidateCompilation(compilation Compilation) error {
	if compilation.ProtocolVersion != CompilerProtocolVersion {
		return fmt.Errorf("%w: protocol version %d", ErrCompilationInvalid, compilation.ProtocolVersion)
	}
	if compilation.Revision == "" || compilation.Encoding == "" || len(compilation.Payload) == 0 || len(compilation.CanonicalYAML) == 0 {
		return fmt.Errorf("%w: revision, encoding, payload, and canonical YAML are required", ErrCompilationInvalid)
	}
	for key, value := range compilation.Facts {
		if key == "" || value == "" {
			return fmt.Errorf("%w: fact names and values must not be empty", ErrCompilationInvalid)
		}
	}
	return nil
}

// ConfigDigest returns the stable identity of target-compiled runtime bytes.
func ConfigDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return digest(sum)
}

// EmbedBytes returns a distinct configured artifact without modifying binary.
func EmbedBytes(binaryImage []byte, compilation Compilation) ([]byte, Inspection, error) {
	inspection, err := InspectBytes(binaryImage)
	if err != nil {
		return nil, Inspection{}, err
	}
	if !inspection.ConfigEmpty {
		return nil, Inspection{}, ErrConfigAlreadyEmbedded
	}
	region, err := findConfigRegion(binaryImage, inspection.Manifest.ConfigRegion.Capacity)
	if err != nil {
		return nil, Inspection{}, err
	}
	encoded, err := encodeConfigRegion(compilation, inspection.Manifest.ConfigRegion.Capacity)
	if err != nil {
		return nil, Inspection{}, err
	}
	configured := append([]byte(nil), binaryImage...)
	copy(configured[region.offset:region.offset+region.capacity], encoded)
	configuredInspection, err := InspectBytes(configured)
	if err != nil {
		return nil, Inspection{}, err
	}
	return configured, configuredInspection, nil
}

// EmbedFile writes a new executable artifact and refuses to overwrite output
// or mutate an already configured input.
func EmbedFile(input, output string, compilation Compilation) (Inspection, error) {
	info, err := os.Stat(input)
	if err != nil {
		return Inspection{}, fmt.Errorf("stat Grove artifact %q: %w", input, err)
	}
	binaryImage, err := os.ReadFile(input)
	if err != nil {
		return Inspection{}, fmt.Errorf("read Grove artifact %q: %w", input, err)
	}
	configured, inspection, err := EmbedBytes(binaryImage, compilation)
	if err != nil {
		return Inspection{}, fmt.Errorf("configure Grove artifact %q: %w", input, err)
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return Inspection{}, fmt.Errorf("create configured Grove artifact %q: %w", output, err)
	}
	removeOutput := true
	defer func() {
		if removeOutput {
			_ = os.Remove(output)
		}
	}()
	if _, err := file.Write(configured); err != nil {
		file.Close()
		return Inspection{}, fmt.Errorf("write configured Grove artifact %q: %w", output, err)
	}
	if err := file.Close(); err != nil {
		return Inspection{}, fmt.Errorf("close configured Grove artifact %q: %w", output, err)
	}
	if err := refreshPlatformSignature(output, configured); err != nil {
		return Inspection{}, err
	}
	inspection, err = InspectFile(output)
	if err != nil {
		return Inspection{}, err
	}
	removeOutput = false
	return inspection, nil
}

func refreshPlatformSignature(path string, binaryImage []byte) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if _, ok := findMachoCodeSignature(binaryImage); !ok {
		return nil
	}
	command := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", path)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("refresh ad-hoc signature for configured Grove artifact %q: %w: %s", path, err, output)
	}
	return nil
}

func encodeConfigRegion(compilation Compilation, capacity int) ([]byte, error) {
	if err := ValidateCompilation(compilation); err != nil {
		return nil, err
	}
	bundle := configBundle{
		FormatVersion: configBundleVersion,
		Revision:      compilation.Revision,
		Encoding:      compilation.Encoding,
		ConfigDigest:  ConfigDigest(compilation.Payload),
		Payload:       compilation.Payload,
		CanonicalYAML: compilation.CanonicalYAML,
		Facts:         compilation.Facts,
	}
	plain, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("encode compiled configuration bundle: %w", err)
	}
	var compressed bytes.Buffer
	compressor, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("create configuration compressor: %w", err)
	}
	compressor.Header.OS = 255
	if _, err := compressor.Write(plain); err != nil {
		return nil, fmt.Errorf("compress compiled configuration: %w", err)
	}
	if err := compressor.Close(); err != nil {
		return nil, fmt.Errorf("finish compiled configuration compression: %w", err)
	}
	required := configRegionHeaderSize + compressed.Len()
	if required > capacity {
		return nil, &OverflowError{Required: required, Capacity: capacity}
	}
	region := make([]byte, capacity)
	copy(region, configRegionMagic)
	binary.BigEndian.PutUint32(region[len(configRegionMagic):configRegionHeaderSize], uint32(compressed.Len()))
	copy(region[configRegionHeaderSize:], compressed.Bytes())
	return region, nil
}

func decodeConfigRegion(data []byte) (configBundle, ConfigMetadata, error) {
	if len(data) < configRegionHeaderSize || string(data[:len(configRegionMagic)]) != configRegionMagic {
		return configBundle{}, ConfigMetadata{}, ErrConfigCorrupt
	}
	compressedSize := int(binary.BigEndian.Uint32(data[len(configRegionMagic):configRegionHeaderSize]))
	if compressedSize <= 0 || configRegionHeaderSize+compressedSize > len(data) {
		return configBundle{}, ConfigMetadata{}, ErrConfigCorrupt
	}
	compressed := data[configRegionHeaderSize : configRegionHeaderSize+compressedSize]
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: decompress header: %v", ErrConfigCorrupt, err)
	}
	plain, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: decompress payload: %v", ErrConfigCorrupt, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	var bundle configBundle
	if err := decoder.Decode(&bundle); err != nil {
		return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: decode bundle: %v", ErrConfigCorrupt, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: bundle has trailing content", ErrConfigCorrupt)
	}
	if bundle.FormatVersion != configBundleVersion {
		return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: bundle version %d", ErrConfigIncompatible, bundle.FormatVersion)
	}
	if bundle.Revision == "" || bundle.Encoding == "" || len(bundle.Payload) == 0 || len(bundle.CanonicalYAML) == 0 {
		return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: bundle fields are incomplete", ErrConfigCorrupt)
	}
	digest := ConfigDigest(bundle.Payload)
	if bundle.ConfigDigest != digest {
		return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: config digest mismatch", ErrConfigCorrupt)
	}
	for _, value := range data[configRegionHeaderSize+compressedSize:] {
		if value != 0 {
			return configBundle{}, ConfigMetadata{}, fmt.Errorf("%w: non-zero bytes follow the bundle", ErrConfigCorrupt)
		}
	}
	return bundle, ConfigMetadata{
		Revision:       bundle.Revision,
		Digest:         digest,
		Encoding:       bundle.Encoding,
		Compression:    "gzip",
		CompiledSize:   len(bundle.Payload),
		CanonicalSize:  len(bundle.CanonicalYAML),
		CompressedSize: compressedSize,
		Facts:          cloneFacts(bundle.Facts),
	}, nil
}

func cloneFacts(facts map[string]string) map[string]string {
	if len(facts) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(facts))
	for key, value := range facts {
		cloned[key] = value
	}
	return cloned
}
