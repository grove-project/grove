package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/artifact"
)

const groveShopConfigEncoding = "gob"

func runConfigCompile(_ context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("config-compile does not accept arguments: %q", args)
	}
	source, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read Grove Shop YAML: %w", err)
	}
	response := artifact.CompilationResponse{ProtocolVersion: artifact.CompilerProtocolVersion}
	configuration, canonical, err := groveshop.CompileConfigurationYAML(source)
	if err != nil {
		failure := &artifact.CompilationFailure{Kind: "validation", Message: err.Error()}
		var validation *groveshop.ConfigurationError
		if errors.As(err, &validation) {
			failure.Field = validation.Field
			failure.Message = validation.Message
		}
		response.Failure = failure
	} else {
		payload, err := groveshop.EncodeConfiguration(configuration)
		if err != nil {
			return fmt.Errorf("encode compiled Grove Shop configuration: %w", err)
		}
		response.Compilation = &artifact.Compilation{
			ProtocolVersion: artifact.CompilerProtocolVersion,
			Revision:        configuration.Revision,
			Encoding:        groveShopConfigEncoding,
			Payload:         payload,
			CanonicalYAML:   canonical,
			Facts: map[string]string{
				"cluster.name": configuration.Cluster.Name,
				"node.zone":    configuration.Node.Zone,
			},
		}
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		return fmt.Errorf("encode configuration compiler response: %w", err)
	}
	return nil
}

func loadEmbeddedGroveShopConfiguration() (artifact.Inspection, groveshop.Configuration, error) {
	inspection, err := inspectEmbeddedGroveShopArtifact()
	if err != nil {
		return artifact.Inspection{}, groveshop.Configuration{}, err
	}
	if inspection.ConfigEmpty {
		configuration := groveshop.DefaultConfiguration()
		payload, err := groveshop.EncodeConfiguration(configuration)
		if err != nil {
			return artifact.Inspection{}, groveshop.Configuration{}, err
		}
		inspection.Config = &artifact.ConfigMetadata{
			Revision: configuration.Revision,
			Digest:   artifact.ConfigDigest(payload),
			Encoding: groveShopConfigEncoding,
		}
		inspection.CompiledConfig = payload
		return inspection, configuration, nil
	}
	if inspection.Config == nil || inspection.Config.Encoding != groveShopConfigEncoding {
		return artifact.Inspection{}, groveshop.Configuration{}, errors.New("embedded Grove Shop configuration encoding is incompatible")
	}
	configuration, err := groveshop.DecodeConfiguration(inspection.CompiledConfig)
	if err != nil {
		return artifact.Inspection{}, groveshop.Configuration{}, err
	}
	if configuration.Revision != inspection.Config.Revision {
		return artifact.Inspection{}, groveshop.Configuration{}, errors.New("embedded Grove Shop configuration revision does not match its metadata")
	}
	return inspection, configuration, nil
}
