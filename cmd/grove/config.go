package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"text/tabwriter"

	"github.com/grove-project/grove/internal/artifact"
)

type targetCompiler func(context.Context, string, []byte) (artifact.Compilation, error)

type targetCompilationError struct {
	failure artifact.CompilationFailure
}

func (e *targetCompilationError) Error() string {
	if e.failure.Field == "" {
		return fmt.Sprintf("target configuration %s failed: %s", e.failure.Kind, e.failure.Message)
	}
	return fmt.Sprintf("target configuration %s failed: %s: %s", e.failure.Kind, e.failure.Field, e.failure.Message)
}

func parseConfigInvocation(args []string, stderr io.Writer) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, errConfigAction
	}
	parsed := invocation{command: commandConfig, configAction: configAction(args[0])}
	if parsed.configAction != configValidate && parsed.configAction != configEmbed && parsed.configAction != configInspect && parsed.configAction != configExtract {
		return invocation{}, fmt.Errorf("parse config action %q: %w", args[0], errConfigAction)
	}
	flags := flag.NewFlagSet("config "+string(parsed.configAction), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&parsed.binaryPath, "binary", "", "Grove application artifact path")
	if parsed.configAction == configValidate || parsed.configAction == configEmbed {
		flags.StringVar(&parsed.configPath, "config", "", "application YAML configuration path")
	}
	if parsed.configAction == configEmbed || parsed.configAction == configExtract {
		flags.StringVar(&parsed.outputPath, "output", "", "new output path")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return invocation{}, fmt.Errorf("parse config %s flags: %w", parsed.configAction, err)
	}
	if flags.NArg() != 0 {
		return invocation{}, fmt.Errorf("parse config %s: %w: %q", parsed.configAction, errUnexpectedArguments, flags.Args())
	}
	if parsed.binaryPath == "" {
		return invocation{}, errBinaryPathRequired
	}
	if (parsed.configAction == configValidate || parsed.configAction == configEmbed) && parsed.configPath == "" {
		return invocation{}, errConfigPathRequired
	}
	if (parsed.configAction == configEmbed || parsed.configAction == configExtract) && parsed.outputPath == "" {
		return invocation{}, errOutputPathRequired
	}
	return parsed, nil
}

func executeConfig(ctx context.Context, parsed invocation, output io.Writer) error {
	return executeConfigWithCompiler(ctx, parsed, output, compileWithTarget)
}

func executeConfigWithCompiler(ctx context.Context, parsed invocation, output io.Writer, compiler targetCompiler) error {
	switch parsed.configAction {
	case configValidate:
		compilation, err := compileConfigFile(ctx, parsed.binaryPath, parsed.configPath, compiler)
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "Configuration\tvalid")
		fmt.Fprintf(writer, "Revision\t%s\n", compilation.Revision)
		fmt.Fprintf(writer, "Config digest\t%s\n", artifact.ConfigDigest(compilation.Payload))
		return writer.Flush()
	case configEmbed:
		compilation, err := compileConfigFile(ctx, parsed.binaryPath, parsed.configPath, compiler)
		if err != nil {
			return err
		}
		inspection, err := artifact.EmbedFile(parsed.binaryPath, parsed.outputPath, compilation)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "Created %s\n", parsed.outputPath)
		return writeArtifactInspection(output, inspection)
	case configInspect:
		inspection, err := artifact.InspectFile(parsed.binaryPath)
		if err != nil {
			return err
		}
		return writeArtifactInspection(output, inspection)
	case configExtract:
		inspection, err := artifact.InspectFile(parsed.binaryPath)
		if err != nil {
			return err
		}
		if inspection.ConfigEmpty {
			return errors.New("artifact does not contain embedded configuration")
		}
		if err := writeNewFile(parsed.outputPath, inspection.CanonicalYAML, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(output, "Extracted %s\n", parsed.outputPath)
		return nil
	default:
		return errConfigAction
	}
}

func compileConfigFile(ctx context.Context, binaryPath, configPath string, compiler targetCompiler) (artifact.Compilation, error) {
	source, err := os.ReadFile(configPath)
	if err != nil {
		return artifact.Compilation{}, fmt.Errorf("read configuration %q: %w", configPath, err)
	}
	compilation, err := compiler(ctx, binaryPath, source)
	if err != nil {
		return artifact.Compilation{}, err
	}
	if err := artifact.ValidateCompilation(compilation); err != nil {
		return artifact.Compilation{}, err
	}
	return compilation, nil
}

func compileWithTarget(ctx context.Context, binaryPath string, source []byte) (artifact.Compilation, error) {
	command := exec.CommandContext(ctx, binaryPath, "config-compile")
	command.Stdin = bytes.NewReader(source)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return artifact.Compilation{}, fmt.Errorf("run target config compiler %q: %w: %s", binaryPath, err, stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	var response artifact.CompilationResponse
	if err := decoder.Decode(&response); err != nil {
		return artifact.Compilation{}, fmt.Errorf("decode target config compiler response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return artifact.Compilation{}, errors.New("target config compiler response has trailing content")
	}
	if response.ProtocolVersion != artifact.CompilerProtocolVersion {
		return artifact.Compilation{}, fmt.Errorf("%w: response protocol version %d", artifact.ErrCompilationInvalid, response.ProtocolVersion)
	}
	if (response.Compilation == nil) == (response.Failure == nil) {
		return artifact.Compilation{}, fmt.Errorf("%w: response must contain exactly one result", artifact.ErrCompilationInvalid)
	}
	if response.Failure != nil {
		return artifact.Compilation{}, &targetCompilationError{failure: *response.Failure}
	}
	if err := artifact.ValidateCompilation(*response.Compilation); err != nil {
		return artifact.Compilation{}, err
	}
	return *response.Compilation, nil
}

func writeArtifactInspection(output io.Writer, inspection artifact.Inspection) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintf(writer, "Application\t%s\n", inspection.Manifest.ApplicationID)
	fmt.Fprintf(writer, "Code version\t%s\n", inspection.Manifest.CodeVersion)
	fmt.Fprintf(writer, "Code digest\t%s\n", inspection.CodeDigest)
	if inspection.ConfigEmpty {
		fmt.Fprintln(writer, "Configuration\tblank")
	} else {
		fmt.Fprintf(writer, "Config revision\t%s\n", inspection.Config.Revision)
		fmt.Fprintf(writer, "Config digest\t%s\n", inspection.Config.Digest)
		fmt.Fprintf(writer, "Config encoding\t%s + %s\n", inspection.Config.Encoding, inspection.Config.Compression)
		keys := make([]string, 0, len(inspection.Config.Facts))
		for key := range inspection.Config.Facts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(writer, "%s\t%s\n", key, inspection.Config.Facts[key])
		}
	}
	fmt.Fprintf(writer, "Artifact digest\t%s\n", inspection.ArtifactDigest)
	return writer.Flush()
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %q: %w", path, err)
	}
	remove = false
	return nil
}
