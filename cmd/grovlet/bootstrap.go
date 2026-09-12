package main

import (
	"fmt"
	"io"

	"github.com/grove-project/grove/internal/bootstrap"
)

func runBootstrapHello(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("bootstrap-hello does not accept arguments: %q", args)
	}
	inspection, configuration, err := loadEmbeddedGroveShopConfiguration()
	if err != nil {
		return fmt.Errorf("load bootstrap artifact identity: %w", err)
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
			ConfigRevision: configuration.Revision,
			ConfigDigest:   inspection.Config.Digest,
			ArtifactDigest: inspection.ArtifactDigest,
			ClusterID:      configuration.Cluster.Name,
			NodeZone:       configuration.Node.Zone,
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
