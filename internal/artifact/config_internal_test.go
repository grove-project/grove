package artifact

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"
)

func TestConfigRegionEncodingIsDeterministic(t *testing.T) {
	compilation := Compilation{
		ProtocolVersion: CompilerProtocolVersion,
		Revision:        "r1",
		Encoding:        "gob",
		Payload:         []byte("compiled"),
		CanonicalYAML:   []byte("revision: r1\n"),
		Facts:           map[string]string{"node.zone": "cloud", "cluster.name": "production"},
	}
	first, err := encodeConfigRegion(compilation, ConfigRegionCapacity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeConfigRegion(compilation, ConfigRegionCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("identical compilations produced different config-region bytes")
	}
}

func TestDecodeConfigRegionRejectsIncompatibleBundle(t *testing.T) {
	bundle := configBundle{
		FormatVersion: configBundleVersion + 1,
		Revision:      "r1",
		Encoding:      "gob",
		ConfigDigest:  ConfigDigest([]byte("compiled")),
		Payload:       []byte("compiled"),
		CanonicalYAML: []byte("revision: r1\n"),
	}
	plain, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	writer.Header.OS = 255
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	region := make([]byte, ConfigRegionCapacity)
	copy(region, configRegionMagic)
	binary.BigEndian.PutUint32(region[len(configRegionMagic):configRegionHeaderSize], uint32(compressed.Len()))
	copy(region[configRegionHeaderSize:], compressed.Bytes())
	if _, _, err := decodeConfigRegion(region); !errors.Is(err, ErrConfigIncompatible) {
		t.Errorf("decode incompatible bundle error = %v; want %v", err, ErrConfigIncompatible)
	}
}
