package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/localcluster"
	"github.com/grove-project/grove/internal/scenario"
	"github.com/grove-project/grove/internal/systemnats"
)

var errDebugDemoTopology = errors.New("application declares no debug demo topology")

const localStateEnvironment = "GROVE_LOCAL_STATE"

type localConnection struct {
	SystemNATSURL string `json:"system_nats_url"`
	NodeID        string `json:"node_id"`
	WebURL        string `json:"web_url"`
}

func executeDeploy(ctx context.Context, parsed invocation, output io.Writer) error {
	delvePath, err := exec.LookPath("dlv")
	if err != nil {
		return fmt.Errorf("locate Delve (install dlv before using --debug-demo): %w", err)
	}
	description, err := describeApplicationScenario(ctx, parsed.binaryPath)
	if err != nil {
		return fmt.Errorf("describe debug demo application: %w", err)
	}
	if description.DebugNodeCount < 1 || len(description.DebugPlacements) == 0 {
		return fmt.Errorf("debug demo application %q: %w", description.ApplicationID, errDebugDemoTopology)
	}
	temporaryDir, err := os.MkdirTemp("", "grove-debug-demo-")
	if err != nil {
		return fmt.Errorf("create debug demo directory: %w", err)
	}
	defer os.RemoveAll(temporaryDir)
	configuredArtifact := filepath.Join(temporaryDir, filepath.Base(parsed.binaryPath))
	compilation, err := compileConfigFile(ctx, parsed.binaryPath, parsed.configPath, compileWithTarget)
	if err != nil {
		return fmt.Errorf("compile debug demo configuration: %w", err)
	}
	inspection, err := artifact.EmbedFile(parsed.binaryPath, configuredArtifact, compilation)
	if err != nil {
		return fmt.Errorf("build configured debug demo artifact: %w", err)
	}
	webPorts, err := localcluster.ReservePorts(1)
	if err != nil {
		return fmt.Errorf("reserve debug demo Web port: %w", err)
	}
	webAddress := "127.0.0.1:" + strconv.Itoa(webPorts[0])
	cluster, err := startDebugDemoCluster(ctx, configuredArtifact, delvePath, description, webAddress)
	if err != nil {
		return err
	}
	defer func() { _ = cluster.Cleanup() }()
	transport, err := systemnats.Connect(ctx, cluster.SystemNATSURL())
	if err != nil {
		return fmt.Errorf("connect to debug demo cluster: %w", err)
	}
	placements := make(map[grove.ServiceID]string, len(description.DebugPlacements))
	for _, component := range description.DebugPlacements {
		placements[component.ServiceID] = component.NodeID
	}
	if err := localcluster.WaitServing(ctx, transport, localcluster.Serving{
		Observer: localcluster.NodeID(0), Nodes: description.DebugNodeCount,
		Placements: placements, ArtifactDigest: inspection.ArtifactDigest, RequireWorker: true,
	}); err != nil {
		transport.Close()
		return fmt.Errorf("wait for debug demo cluster: %w\n%s", err, cluster.Diagnostics())
	}
	transport.Close()
	systemNATSURL := cluster.SystemNATSURL()
	state := localConnection{SystemNATSURL: systemNATSURL, NodeID: "node-1", WebURL: "http://" + webAddress}
	if err := writeLocalConnection(state); err != nil {
		return err
	}
	defer removeLocalConnection()
	fmt.Fprintf(output, "%s debug demo ready\n", description.Name)
	fmt.Fprintf(output, "%-8s %s\n", "Artifact", inspection.ArtifactDigest)
	for _, component := range description.DebugPlacements {
		if component.Ingress {
			fmt.Fprintf(output, "%-8s %s\n", component.Name, state.WebURL)
			break
		}
	}
	for _, component := range description.DebugPlacements {
		fmt.Fprintf(output, "%-8s %s\n", component.Name, component.NodeID)
	}
	fmt.Fprintln(output, "Keep this command running; press Ctrl-C to stop the cluster.")
	<-ctx.Done()
	return nil
}

// startDebugDemoCluster starts the application's fixed debug-demo topology.
// Every HTTP ingress component listens on webAddress.
func startDebugDemoCluster(
	ctx context.Context,
	artifactPath string,
	delvePath string,
	description scenario.Description,
	webAddress string,
) (*localcluster.Cluster, error) {
	components := make([]localcluster.Component, 0, len(description.DebugPlacements))
	for _, component := range description.DebugPlacements {
		components = append(components, localcluster.Component{
			NodeID: component.NodeID, Kind: component.Kind, Options: component.Options, Ingress: component.Ingress,
		})
	}
	cluster, err := localcluster.Start(ctx, artifactPath, localcluster.Spec{
		Nodes: description.DebugNodeCount, Components: components, IngressAddress: webAddress,
		SubjectRoot: "_GROVE.system.debug-demo.", DelvePath: delvePath,
	})
	if err != nil {
		return nil, fmt.Errorf("start debug demo cluster: %w", err)
	}
	return cluster, nil
}

func localStatePath() string {
	if path := os.Getenv(localStateEnvironment); path != "" {
		return path
	}
	return filepath.Join(".grove", "debug-demo.json")
}

func writeLocalConnection(connection localConnection) error {
	return writeLocalConnectionFile(localStatePath(), connection)
}

func writeLocalConnectionFile(path string, connection localConnection) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create local Grove state directory: %w", err)
	}
	encoded, err := json.Marshal(connection)
	if err != nil {
		return fmt.Errorf("encode local Grove connection: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write local Grove connection %q: %w", path, err)
	}
	return nil
}

func readLocalConnection() (localConnection, error) {
	path := localStatePath()
	encoded, err := os.ReadFile(path)
	if err != nil {
		return localConnection{}, err
	}
	var connection localConnection
	if err := json.Unmarshal(encoded, &connection); err != nil {
		return localConnection{}, fmt.Errorf("decode local Grove connection %q: %w", path, err)
	}
	return connection, nil
}

func removeLocalConnection() {
	path := localStatePath()
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))
}

func applyLocalConnection(parsed *invocation) {
	if parsed.systemNATSURL != "" && parsed.nodeID != "" {
		return
	}
	connection, err := readLocalConnection()
	if err != nil {
		return
	}
	if parsed.systemNATSURL == "" {
		parsed.systemNATSURL = connection.SystemNATSURL
	}
	if parsed.nodeID == "" {
		parsed.nodeID = connection.NodeID
	}
}
