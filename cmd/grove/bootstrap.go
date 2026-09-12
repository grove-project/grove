package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/grove-project/grove/internal/bootstrap"
)

type bootstrapHelloReader func(context.Context, string) (bootstrap.Hello, error)

func negotiateBootstrapTargets(ctx context.Context, currentPath, candidatePath string) (bootstrap.Negotiation, error) {
	return negotiateBootstrapTargetsWithReader(ctx, currentPath, candidatePath, readTargetBootstrapHello)
}

func negotiateBootstrapTargetsWithReader(ctx context.Context, currentPath, candidatePath string, reader bootstrapHelloReader) (bootstrap.Negotiation, error) {
	current, err := reader(ctx, currentPath)
	if err != nil {
		return bootstrap.Negotiation{}, fmt.Errorf("read current bootstrap hello: %w", err)
	}
	candidate, err := reader(ctx, candidatePath)
	if err != nil {
		return bootstrap.Negotiation{}, fmt.Errorf("read candidate bootstrap hello: %w", err)
	}
	negotiation, err := bootstrap.Negotiate(current, candidate)
	if err != nil {
		return bootstrap.Negotiation{}, fmt.Errorf("negotiate current and candidate bootstrap: %w", err)
	}
	return negotiation, nil
}

func readTargetBootstrapHello(ctx context.Context, binaryPath string) (bootstrap.Hello, error) {
	command := exec.CommandContext(ctx, binaryPath, "bootstrap-hello")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return bootstrap.Hello{}, fmt.Errorf("run target bootstrap hello %q: %w: %s", binaryPath, err, stderr.String())
	}
	hello, err := bootstrap.UnmarshalHello(stdout.Bytes())
	if err != nil {
		return bootstrap.Hello{}, fmt.Errorf("decode target bootstrap hello %q: %w", binaryPath, err)
	}
	return hello, nil
}
