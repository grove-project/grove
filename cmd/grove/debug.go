package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/grove-project/grove/internal/debuggateway"
	"github.com/grove-project/grove/internal/systemnats"
)

func executeDebug(ctx context.Context, parsed invocation, transport *systemnats.Transport, output io.Writer) error {
	session, err := debuggateway.Open(ctx, transport, parsed.nodeID, parsed.serviceName, parsed.listenAddress)
	if err != nil {
		return err
	}
	defer session.Close()
	target := session.Target
	fmt.Fprintf(output, "Service  %s (%d)\n", strings.ToLower(target.ServiceName), target.ServiceID)
	fmt.Fprintf(output, "Node     %s\n", target.NodeID)
	fmt.Fprintf(output, "Worker   %s\n", target.WorkerID)
	fmt.Fprintf(output, "Artifact %s\n", target.ArtifactDigest)
	fmt.Fprintf(output, "Version  %s\n", target.CodeVersion)
	fmt.Fprintf(output, "DAP listening locally on %s\n", session.Address)
	return session.Wait(ctx)
}
