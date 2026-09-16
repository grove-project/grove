package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/debuggateway"
	"github.com/grove-project/grove/internal/systemnats"
)

var errDebugServiceRequired = errors.New("debug service name is required")

type debugAttachResult struct {
	ServiceID      grove.ServiceID `json:"service_id"`
	ServiceName    string          `json:"service_name"`
	NodeID         string          `json:"node_id"`
	WorkerID       string          `json:"worker_id"`
	ArtifactDigest string          `json:"artifact_digest"`
	CodeVersion    string          `json:"code_version"`
	DAPEndpoint    string          `json:"dap_endpoint"`
}

type debugAttachAction struct {
	result    debugAttachResult
	session   *debuggateway.Session
	transport *systemnats.Transport
}

func (a *debugAttachAction) InitialResult() any {
	return a.result
}

func (a *debugAttachAction) Wait(ctx context.Context) error {
	defer a.transport.Close()
	return a.session.Wait(ctx)
}

func (c *applicationController) attachDebugger(ctx context.Context, args []string) (any, error) {
	serviceName, listenAddress, err := parseDebugAttachArguments(args)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()
	if cluster == nil {
		return nil, errApplicationNotDeployed
	}
	if !cluster.debugDemo {
		return nil, errors.New("start the five-node debug demo before attaching a debugger")
	}
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return nil, fmt.Errorf("connect to debug demo cluster: %w", err)
	}
	session, err := debuggateway.Open(ctx, transport, "node-1", serviceName, listenAddress)
	if err != nil {
		transport.Close()
		return nil, err
	}
	target := session.Target
	return &debugAttachAction{
		result: debugAttachResult{
			ServiceID: target.ServiceID, ServiceName: target.ServiceName,
			NodeID: target.NodeID, WorkerID: target.WorkerID,
			ArtifactDigest: target.ArtifactDigest, CodeVersion: target.CodeVersion,
			DAPEndpoint: session.Address,
		},
		session: session, transport: transport,
	}, nil
}

func parseDebugAttachArguments(args []string) (string, string, error) {
	if len(args) == 0 || args[0] == "" {
		return "", "", errDebugServiceRequired
	}
	serviceName := args[0]
	flags := flag.NewFlagSet("debug.attach", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listenAddress := "127.0.0.1:0"
	flags.StringVar(&listenAddress, "listen", listenAddress, "local DAP listen address")
	if err := flags.Parse(args[1:]); err != nil {
		return "", "", fmt.Errorf("parse debug.attach arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return "", "", fmt.Errorf("parse debug.attach arguments: %w: %q", errConsoleArguments, flags.Args())
	}
	return serviceName, listenAddress, nil
}
