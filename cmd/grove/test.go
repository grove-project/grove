package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/localcluster"
	"github.com/grove-project/grove/internal/scenario"
	"github.com/grove-project/grove/internal/systemnats"
)

const testCommandTimeout = 60 * time.Second

// commandTestCluster is the cluster `grove test` runs an application's
// end-to-end check against: one node per check component, in order, and a
// final check node that hosts no component and routes the check's calls.
type commandTestCluster struct {
	binaryPath string
	local      *localcluster.Cluster
	components []scenario.Component
}

func (c *commandTestCluster) checkNodeID() string {
	return localcluster.NodeID(c.local.Len() - 1)
}

func executeTest(ctx context.Context, parsed invocation, output io.Writer) error {
	inspection, err := artifact.InspectFile(parsed.binaryPath)
	if err != nil {
		return fmt.Errorf("inspect test artifact: %w", err)
	}
	description, err := describeApplicationScenario(ctx, parsed.binaryPath)
	if err != nil {
		return fmt.Errorf("test artifact application %q: %w", inspection.Manifest.ApplicationID, errors.Join(errTestApplication, err))
	}
	if len(description.CheckComponents) == 0 {
		return fmt.Errorf("test artifact application %q: %w", inspection.Manifest.ApplicationID, errors.Join(errTestApplication, scenario.ErrNoCheck))
	}
	serviceID := parsed.serviceID
	if serviceID == 0 {
		serviceID = description.RecoveryServiceID
	}
	if parsed.resilience && serviceID == 0 {
		return fmt.Errorf("test artifact application %q declares no recovery service: %w", inspection.Manifest.ApplicationID, errServiceIDRequired)
	}
	cluster, err := startCommandTestCluster(ctx, parsed.binaryPath, description.CheckComponents, parsed.resilience)
	if err != nil {
		return err
	}
	fail := func(operation string, operationErr error) error {
		diagnostics := cluster.local.Diagnostics()
		cleanupErr := cluster.local.Cleanup()
		return fmt.Errorf("%s: %w\n%s", operation, errors.Join(operationErr, cleanupErr), diagnostics)
	}
	transport, err := systemnats.Connect(ctx, cluster.local.SystemNATSURL())
	if err != nil {
		return fail("connect to test cluster", err)
	}
	if err := localcluster.WaitServing(ctx, transport, cluster.serving(inspection.ArtifactDigest)); err != nil {
		transport.Close()
		return fail("wait for test cluster", err)
	}
	transport.Close()
	summary, err := runCommandTestCheck(ctx, cluster, "grove-test-order")
	if err != nil {
		return fail("run "+description.Name+" check", err)
	}
	failedNodeID := ""
	recoveredNodeID := ""
	if parsed.resilience {
		failedNodeID, recoveredNodeID, err = runCommandTestResilience(ctx, cluster, serviceID, inspection.ArtifactDigest)
		if err != nil {
			return fail("run resilience scenario", err)
		}
		if _, err := runCommandTestCheck(ctx, cluster, "grove-test-order-after-recovery"); err != nil {
			return fail("rerun "+description.Name+" check", err)
		}
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = cluster.local.Stop(stopCtx, failedNodeID)
	cancel()
	if err != nil {
		return fail("stop test cluster", err)
	}
	if err := cluster.local.Cleanup(); err != nil {
		return fmt.Errorf("clean up test cluster: %w", err)
	}
	transcript := fmt.Sprintf("%s E2E\n✓ cluster ready\n✓ %s\nPASS\n", description.Name, summary)
	if parsed.resilience {
		transcript = fmt.Sprintf(
			"%s E2E\n✓ baseline\nKilled service %d host %s\n✓ failure detected\n✓ service %d recovered on %s\n✓ flow after recovery\nPASS\n",
			description.Name,
			serviceID,
			failedNodeID,
			serviceID,
			recoveredNodeID,
		)
	}
	_, err = io.WriteString(output, transcript)
	return err
}

// describeApplicationScenario asks the application binary for its scenario,
// so the CLI never links application code.
func describeApplicationScenario(ctx context.Context, binaryPath string) (scenario.Description, error) {
	var description scenario.Description
	if err := runApplicationScenarioCommand(ctx, binaryPath, &description, scenario.DescribeCommand); err != nil {
		return scenario.Description{}, err
	}
	if description.ProtocolVersion != scenario.ProtocolVersion {
		return scenario.Description{}, fmt.Errorf("scenario protocol version %d is unsupported", description.ProtocolVersion)
	}
	return description, nil
}

// runCommandTestCheck runs the application's own end-to-end check, inside
// the application binary, against the cluster through the check node.
func runCommandTestCheck(ctx context.Context, cluster *commandTestCluster, runID string) (string, error) {
	var result scenario.CheckResult
	if err := runApplicationScenarioCommand(
		ctx,
		cluster.binaryPath,
		&result,
		scenario.CheckCommand,
		"--system-nats-url", cluster.local.SystemNATSURL(),
		"--node-id", cluster.checkNodeID(),
		"--run-id", runID,
	); err != nil {
		return "", err
	}
	if result.ProtocolVersion != scenario.ProtocolVersion {
		return "", fmt.Errorf("scenario protocol version %d is unsupported", result.ProtocolVersion)
	}
	return result.Summary, nil
}

func runApplicationScenarioCommand(ctx context.Context, binaryPath string, response any, args ...string) error {
	command := exec.CommandContext(ctx, binaryPath, append([]string{scenario.Command}, args...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w: %s", scenario.Command, args[0], err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(stdout.Bytes(), response); err != nil {
		return fmt.Errorf("decode %s %s response: %w", scenario.Command, args[0], err)
	}
	return nil
}

// runCommandTestResilience kills the node hosting serviceID and waits for
// the service to recover on another node. It returns the failed and
// recovered node IDs.
func runCommandTestResilience(
	ctx context.Context,
	cluster *commandTestCluster,
	serviceID grove.ServiceID,
	artifactDigest string,
) (string, string, error) {
	transport, err := systemnats.Connect(ctx, cluster.local.SystemNATSURL())
	if err != nil {
		return "", "", err
	}
	placement, err := transport.RequestPlacement(ctx, cluster.checkNodeID())
	transport.Close()
	if err != nil {
		return "", "", err
	}
	targetNodeID := ""
	for _, record := range placement.Placements {
		if record.ServiceID == serviceID {
			targetNodeID = record.NodeID
			break
		}
	}
	if targetNodeID == "" {
		return "", "", fmt.Errorf("service %d is not placed", serviceID)
	}
	if _, ok := cluster.local.Node(targetNodeID); !ok {
		return "", "", fmt.Errorf("service %d host %q is not a test node", serviceID, targetNodeID)
	}
	if err := cluster.local.Kill(ctx, targetNodeID); err != nil {
		return targetNodeID, "", err
	}
	survivor := localcluster.NodeID(0)
	if survivor == targetNodeID {
		survivor = localcluster.NodeID(1)
	}
	if err := cluster.local.UseSystemNATSOf(survivor); err != nil {
		return targetNodeID, "", err
	}
	transport, err = systemnats.Connect(ctx, cluster.local.SystemNATSURL())
	if err != nil {
		return targetNodeID, "", err
	}
	defer transport.Close()
	recoveredNodeID, err := localcluster.WaitRecovery(ctx, transport, localcluster.Recovery{
		Observer: cluster.checkNodeID(), Nodes: cluster.local.Len(), FailedNodeID: targetNodeID,
		ServiceID: serviceID, ArtifactDigest: artifactDigest,
	})
	if err != nil {
		return targetNodeID, "", err
	}
	return targetNodeID, recoveredNodeID, nil
}

// startCommandTestCluster starts one node per check component, in order, and
// a final check node that hosts no component.
func startCommandTestCluster(ctx context.Context, binaryPath string, components []scenario.Component, resilience bool) (*commandTestCluster, error) {
	placed := make([]localcluster.Component, len(components))
	for i, component := range components {
		placed[i] = localcluster.Component{NodeID: localcluster.NodeID(i), Kind: component.Kind}
	}
	local, err := localcluster.Start(ctx, binaryPath, localcluster.Spec{
		Nodes: len(components) + 1, Components: placed,
		SubjectRoot: "_GROVE.system.test.", Recovery: resilience,
	})
	if err != nil {
		return nil, fmt.Errorf("start test cluster: %w", err)
	}
	return &commandTestCluster{binaryPath: binaryPath, local: local, components: components}, nil
}

// serving is when the test cluster serves artifactDigest: every check
// component placed and healthy, and every node, not just the check node,
// serving its own cluster and placement views.
func (c *commandTestCluster) serving(artifactDigest string) localcluster.Serving {
	placements := make(map[grove.ServiceID]string, len(c.components))
	for _, component := range c.components {
		placements[component.ServiceID] = ""
	}
	return localcluster.Serving{
		Observer: c.checkNodeID(), Nodes: c.local.Len(), Placements: placements,
		ArtifactDigest: artifactDigest, NodeIDs: c.local.NodeIDs(),
	}
}
