package main

import (
	"fmt"
	"io"

	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/bootstrap"
)

func runBootstrapHello(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("bootstrap-hello does not accept arguments: %q", args)
	}
	inspection, err := inspectEmbeddedGroveShopArtifact()
	if err != nil {
		return fmt.Errorf("load bootstrap artifact identity: %w", err)
	}
	config := inspection.Config
	if inspection.ConfigEmpty {
		configuration := groveshop.DefaultConfiguration()
		payload, err := groveshop.EncodeConfiguration(configuration)
		if err != nil {
			return fmt.Errorf("load default bootstrap configuration identity: %w", err)
		}
		config = &artifact.ConfigMetadata{
			Revision: configuration.Revision,
			Digest:   artifact.ConfigDigest(payload),
			Facts: map[string]string{
				"cluster.name": configuration.Cluster.Name,
				"node.zone":    configuration.Node.Zone,
			},
		}
	}
	encoded, err := bootstrap.MarshalHello(bootstrap.Hello{
		BootstrapVersions: []int{bootstrap.BootstrapVersion},
		Capabilities: []string{
			bootstrap.CapabilityArtifactIdentity,
			bootstrap.CapabilitySideBySide,
			bootstrap.CapabilityReadiness,
		},
		Artifact: bootstrap.ArtifactIdentity{
			ApplicationID:  inspection.Manifest.ApplicationID,
			RuntimeVersion: inspection.Manifest.CodeVersion,
			CodeDigest:     inspection.CodeDigest,
			ConfigRevision: config.Revision,
			ConfigDigest:   config.Digest,
			ArtifactDigest: inspection.ArtifactDigest,
			ClusterID:      config.Facts["cluster.name"],
			NodeZone:       config.Facts["node.zone"],
		},
	})
	if err != nil {
		return err
	}
	if _, err := stdout.Write(encoded); err != nil {
		return fmt.Errorf("write bootstrap hello: %w", err)
	}
	return nil
}
