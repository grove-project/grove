// Package artifact defines and inspects Grove's immutable application artifact
// envelope.
package artifact

import (
	"bytes"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/grove-project/grove"
)

const (
	// FormatVersion is the artifact manifest format understood by this runtime.
	FormatVersion = 1
	// ConfigFormatVersion is the reserved configuration-region format version.
	ConfigFormatVersion = 1
	// ConfigRegionCapacity is the number of payload bytes reserved in an MVP
	// artifact. Task 024 defines the payload stored in this fixed region.
	ConfigRegionCapacity = 4096

	// ManifestPrefix frames the beginning of an embedded artifact manifest.
	ManifestPrefix = "\x00GROVE_ARTIFACT_MANIFEST_V1_BEGIN\x00"
	// ManifestSuffix frames the end of an embedded artifact manifest.
	ManifestSuffix = "\x00GROVE_ARTIFACT_MANIFEST_V1_END\x00"
	// ConfigRegionPrefix frames the beginning of the reserved config bytes.
	ConfigRegionPrefix = "\x00GROVE_ARTIFACT_CONFIG_V1_BEGIN\x00"
	// ConfigRegionSuffix frames the end of the reserved config bytes.
	ConfigRegionSuffix = "\x00GROVE_ARTIFACT_CONFIG_V1_END\x00"
)

var (
	// ErrManifestNotFound is returned when an executable has no valid Grove
	// artifact manifest envelope.
	ErrManifestNotFound = errors.New("grove artifact manifest not found")
	// ErrManifestInvalid is returned when embedded artifact metadata violates
	// the supported contract.
	ErrManifestInvalid = errors.New("grove artifact manifest is invalid")
	// ErrConfigRegionNotFound is returned when an artifact has no fixed config
	// reservation matching its manifest.
	ErrConfigRegionNotFound = errors.New("grove artifact config region not found")
)

// Manifest describes the application and runtime contents compiled into one
// immutable Grove artifact.
type Manifest struct {
	FormatVersion int          `json:"format_version"`
	ApplicationID string       `json:"application_id"`
	CodeVersion   string       `json:"code_version"`
	Components    []Component  `json:"components"`
	UIAssets      []string     `json:"ui_assets"`
	ConfigRegion  ConfigRegion `json:"config_region"`
}

// Component declares one application component entrypoint present in an
// artifact.
type Component struct {
	ServiceID  grove.ServiceID `json:"service_id"`
	Name       string          `json:"name"`
	Runtime    string          `json:"runtime"`
	Entrypoint []string        `json:"entrypoint"`
}

// ConfigRegion describes the fixed-capacity customer-configuration reservation
// without defining its later payload semantics.
type ConfigRegion struct {
	FormatVersion int `json:"format_version"`
	Capacity      int `json:"capacity"`
}

// Inspection is the verified metadata and identity of one executable artifact.
type Inspection struct {
	Manifest       Manifest
	MetadataDigest string
	CodeDigest     string
	ArtifactDigest string
	ConfigEmpty    bool
	Config         *ConfigMetadata
	CompiledConfig []byte
	CanonicalYAML  []byte
}

// InspectFile reads path as an immutable Grove artifact and verifies its
// embedded manifest and reserved configuration region.
func InspectFile(path string) (Inspection, error) {
	binary, err := os.ReadFile(path)
	if err != nil {
		return Inspection{}, fmt.Errorf("read Grove artifact %q: %w", path, err)
	}
	inspection, err := InspectBytes(binary)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect Grove artifact %q: %w", path, err)
	}
	return inspection, nil
}

// InspectBytes verifies one binary image and calculates stable SHA-256
// identities for its exact manifest and complete bytes.
func InspectBytes(binary []byte) (Inspection, error) {
	manifest, encoded, err := findManifest(binary)
	if err != nil {
		return Inspection{}, err
	}
	config, err := findConfigRegion(binary, manifest.ConfigRegion.Capacity)
	if err != nil {
		return Inspection{}, err
	}
	metadataSum := sha256.Sum256(encoded)
	artifactSum := sha256.Sum256(binary)
	inspection := Inspection{
		Manifest:       manifest,
		MetadataDigest: digest(metadataSum),
		CodeDigest:     normalizedCodeDigest(binary, config),
		ArtifactDigest: digest(artifactSum),
		ConfigEmpty:    allZero(config.data),
	}
	if inspection.ConfigEmpty {
		return inspection, nil
	}
	bundle, metadata, err := decodeConfigRegion(config.data)
	if err != nil {
		return Inspection{}, err
	}
	inspection.Config = &metadata
	inspection.CompiledConfig = append([]byte(nil), bundle.Payload...)
	inspection.CanonicalYAML = append([]byte(nil), bundle.CanonicalYAML...)
	return inspection, nil
}

func findManifest(binary []byte) (Manifest, []byte, error) {
	var (
		found        Manifest
		encoded      []byte
		matches      int
		candidateErr error
	)
	for offset := 0; offset < len(binary); {
		start := bytes.Index(binary[offset:], []byte(ManifestPrefix))
		if start < 0 {
			break
		}
		start += offset + len(ManifestPrefix)
		end := bytes.Index(binary[start:], []byte(ManifestSuffix))
		if end >= 0 {
			candidate := binary[start : start+end]
			manifest, err := decodeManifest(candidate)
			if err != nil {
				candidateErr = err
			} else {
				found = manifest
				encoded = append(encoded[:0], candidate...)
				matches++
			}
		}
		offset = start
	}
	if matches == 0 {
		if candidateErr != nil {
			return Manifest{}, nil, candidateErr
		}
		return Manifest{}, nil, ErrManifestNotFound
	}
	if matches != 1 {
		return Manifest{}, nil, fmt.Errorf("%w: found %d valid manifests", ErrManifestInvalid, matches)
	}
	return found, encoded, nil
}

func decodeManifest(encoded []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode metadata: %v", ErrManifestInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("%w: metadata has trailing content", ErrManifestInvalid)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.FormatVersion != FormatVersion {
		return fmt.Errorf("%w: unsupported format version %d", ErrManifestInvalid, manifest.FormatVersion)
	}
	if manifest.ApplicationID == "" || manifest.CodeVersion == "" || len(manifest.Components) == 0 || len(manifest.UIAssets) == 0 {
		return fmt.Errorf("%w: application, code version, components, and UI assets are required", ErrManifestInvalid)
	}
	if manifest.ConfigRegion.FormatVersion != ConfigFormatVersion || manifest.ConfigRegion.Capacity != ConfigRegionCapacity {
		return fmt.Errorf("%w: config region must use format %d and capacity %d", ErrManifestInvalid, ConfigFormatVersion, ConfigRegionCapacity)
	}
	services := make(map[grove.ServiceID]struct{}, len(manifest.Components))
	for _, component := range manifest.Components {
		if component.ServiceID == 0 || component.Name == "" || component.Runtime == "" || len(component.Entrypoint) == 0 {
			return fmt.Errorf("%w: component declaration is incomplete", ErrManifestInvalid)
		}
		if _, exists := services[component.ServiceID]; exists {
			return fmt.Errorf("%w: duplicate service ID %d", ErrManifestInvalid, component.ServiceID)
		}
		services[component.ServiceID] = struct{}{}
		for _, argument := range component.Entrypoint {
			if argument == "" {
				return fmt.Errorf("%w: component %d has an empty entrypoint argument", ErrManifestInvalid, component.ServiceID)
			}
		}
	}
	assets := make(map[string]struct{}, len(manifest.UIAssets))
	for _, path := range manifest.UIAssets {
		if path == "" {
			return fmt.Errorf("%w: UI asset path is empty", ErrManifestInvalid)
		}
		if _, exists := assets[path]; exists {
			return fmt.Errorf("%w: duplicate UI asset %q", ErrManifestInvalid, path)
		}
		assets[path] = struct{}{}
	}
	return nil
}

type configRegion struct {
	offset   int
	capacity int
	data     []byte
}

func findConfigRegion(binary []byte, capacity int) (configRegion, error) {
	prefix := []byte(ConfigRegionPrefix)
	suffix := []byte(ConfigRegionSuffix)
	var (
		region  configRegion
		matches int
	)
	for offset := 0; offset < len(binary); {
		start := bytes.Index(binary[offset:], prefix)
		if start < 0 {
			break
		}
		start += offset + len(prefix)
		end := start + capacity
		if end+len(suffix) <= len(binary) && bytes.Equal(binary[end:end+len(suffix)], suffix) {
			region = configRegion{offset: start, capacity: capacity, data: binary[start:end]}
			matches++
		}
		offset = start
	}
	if matches == 0 {
		return configRegion{}, ErrConfigRegionNotFound
	}
	if matches != 1 {
		return configRegion{}, fmt.Errorf("%w: found %d valid config regions", ErrManifestInvalid, matches)
	}
	return region, nil
}

func normalizedCodeDigest(binary []byte, region configRegion) string {
	normalized := append([]byte(nil), binary...)
	clear(normalized[region.offset : region.offset+region.capacity])
	if signature, ok := findMachoCodeSignature(binary); ok {
		// Ad-hoc re-signing changes both the signature blob and the size fields
		// describing it. Neither is application code, so exclude both from the
		// stable code identity.
		clear(normalized[signature.commandOffset : signature.commandOffset+16])
		for _, field := range signature.linkeditSizeFields {
			clear(normalized[field.offset : field.offset+field.size])
		}
		normalized = append(normalized[:signature.dataOffset], normalized[signature.dataOffset+signature.dataSize:]...)
	}
	sum := sha256.Sum256(normalized)
	return digest(sum)
}

type byteRange struct {
	offset int
	size   int
}

type machoSignature struct {
	commandOffset      int
	dataOffset         int
	dataSize           int
	linkeditSizeFields []byteRange
}

func findMachoCodeSignature(binaryImage []byte) (machoSignature, bool) {
	file, err := macho.NewFile(bytes.NewReader(binaryImage))
	if err != nil {
		return machoSignature{}, false
	}
	defer file.Close()
	headerSize := 28
	if file.Magic == macho.Magic64 {
		headerSize = 32
	}
	const (
		loadCommandSegment       = 0x1
		loadCommandSegment64     = 0x19
		loadCommandCodeSignature = 0x1d
	)
	var signature machoSignature
	offset := headerSize
	for command := uint32(0); command < file.Ncmd; command++ {
		if offset+8 > len(binaryImage) {
			return machoSignature{}, false
		}
		kind := file.ByteOrder.Uint32(binaryImage[offset : offset+4])
		size := int(file.ByteOrder.Uint32(binaryImage[offset+4 : offset+8]))
		if size < 8 || offset+size > len(binaryImage) {
			return machoSignature{}, false
		}
		if (kind == loadCommandSegment || kind == loadCommandSegment64) && size >= 48 && string(bytes.TrimRight(binaryImage[offset+8:offset+24], "\x00")) == "__LINKEDIT" {
			if kind == loadCommandSegment64 && size >= 56 {
				signature.linkeditSizeFields = append(signature.linkeditSizeFields, byteRange{offset: offset + 32, size: 8}, byteRange{offset: offset + 48, size: 8})
			} else if kind == loadCommandSegment {
				signature.linkeditSizeFields = append(signature.linkeditSizeFields, byteRange{offset: offset + 28, size: 4}, byteRange{offset: offset + 36, size: 4})
			}
		}
		if kind == loadCommandCodeSignature && size >= 16 {
			signature.commandOffset = offset
			signature.dataOffset = int(file.ByteOrder.Uint32(binaryImage[offset+8 : offset+12]))
			signature.dataSize = int(file.ByteOrder.Uint32(binaryImage[offset+12 : offset+16]))
		}
		offset += size
	}
	if signature.dataOffset <= 0 || signature.dataSize <= 0 || signature.dataOffset+signature.dataSize > len(binaryImage) {
		return machoSignature{}, false
	}
	return signature, true
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func digest(sum [sha256.Size]byte) string {
	return "sha256:" + hex.EncodeToString(sum[:])
}
