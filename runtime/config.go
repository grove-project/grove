package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/grove-project/grove/internal/artifact"
)

func runConfigCompile(_ context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("config-compile does not accept arguments: %q", args)
	}
	source, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read application YAML: %w", err)
	}
	response := artifact.CompilationResponse{ProtocolVersion: artifact.CompilerProtocolVersion}
	configuration, err := activeApplication.Configuration.Compile(source)
	if err != nil {
		failure := &artifact.CompilationFailure{Kind: "validation", Message: err.Error()}
		if activeApplication.Configuration.Failure != nil {
			failure.Field, failure.Message = activeApplication.Configuration.Failure(err)
		}
		response.Failure = failure
	} else {
		if err := validateApplicationConfiguration(configuration); err != nil {
			return err
		}
		response.Compilation = &artifact.Compilation{
			ProtocolVersion: artifact.CompilerProtocolVersion,
			Revision:        configuration.Revision,
			Encoding:        configuration.Encoding,
			Payload:         configuration.Payload,
			CanonicalYAML:   configuration.CanonicalYAML,
			Facts:           configuration.Facts,
		}
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		return fmt.Errorf("encode configuration compiler response: %w", err)
	}
	return nil
}

func loadEmbeddedConfiguration() (artifact.Inspection, Configuration, error) {
	inspection, err := inspectEmbeddedArtifact()
	if err != nil {
		return artifact.Inspection{}, Configuration{}, err
	}
	if inspection.ConfigEmpty {
		configuration, err := activeApplication.Configuration.Default()
		if err != nil {
			return artifact.Inspection{}, Configuration{}, err
		}
		if err := validateApplicationConfiguration(configuration); err != nil {
			return artifact.Inspection{}, Configuration{}, err
		}
		inspection.Config = &artifact.ConfigMetadata{
			Revision: configuration.Revision,
			Digest:   artifact.ConfigDigest(configuration.Payload),
			Encoding: configuration.Encoding,
			Facts:    configuration.Facts,
		}
		inspection.CompiledConfig = configuration.Payload
		return inspection, configuration, nil
	}
	if inspection.Config == nil {
		return artifact.Inspection{}, Configuration{}, errors.New("embedded application configuration metadata is missing")
	}
	configuration, err := activeApplication.Configuration.Decode(inspection.CompiledConfig)
	if err != nil {
		return artifact.Inspection{}, Configuration{}, err
	}
	if err := validateApplicationConfiguration(configuration); err != nil {
		return artifact.Inspection{}, Configuration{}, err
	}
	if configuration.Encoding != inspection.Config.Encoding {
		return artifact.Inspection{}, Configuration{}, errors.New("embedded application configuration encoding is incompatible")
	}
	if configuration.Revision != inspection.Config.Revision {
		return artifact.Inspection{}, Configuration{}, errors.New("embedded application configuration revision does not match its metadata")
	}
	return inspection, configuration, nil
}

func validateApplicationConfiguration(configuration Configuration) error {
	if configuration.Value == nil || configuration.Revision == "" || configuration.Encoding == "" || len(configuration.Payload) == 0 {
		return fmt.Errorf("%w: compiled configuration is incomplete", ErrApplicationDefinitionInvalid)
	}
	return nil
}
