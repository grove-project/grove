// Command grove is the operator entry point for a Grove cluster.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const commandTimeout = 10 * time.Second

var (
	errCommandRequired       = errors.New("command is required")
	errCommandUnknown        = errors.New("command is unknown")
	errComponentAction       = errors.New("component action must be start or stop")
	errSystemNATSURLRequired = errors.New("system NATS URL is required")
	errNodeIDRequired        = errors.New("node ID is required")
	errServiceIDRequired     = errors.New("service ID must be greater than zero")
	errComponentNotFound     = errors.New("component is not present in the node view")
	errUnexpectedArguments   = errors.New("unexpected arguments")
)

type commandName string

const (
	commandStatus     commandName = "status"
	commandNodes      commandName = "nodes"
	commandComponents commandName = "components"
	commandComponent  commandName = "component"
)

type componentAction string

const (
	componentStart componentAction = "start"
	componentStop  componentAction = "stop"
)

type invocation struct {
	command       commandName
	action        componentAction
	systemNATSURL string
	nodeID        string
	serviceID     grove.ServiceID
}

type controlClient interface {
	RequestClusterView(context.Context, string) (systemnats.ClusterView, error)
	RequestComponents(context.Context, string) (systemnats.ComponentView, error)
	RequestStartComponent(context.Context, string, grove.ServiceID) (systemnats.ComponentView, error)
	RequestStopComponent(context.Context, string, grove.ServiceID) (systemnats.ComponentView, error)
}

func main() {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, commandTimeout)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "grove: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	invocation, err := parseInvocation(args, stderr)
	if err != nil {
		return err
	}
	client, err := systemnats.Connect(ctx, invocation.systemNATSURL)
	if err != nil {
		return err
	}
	defer client.Close()
	return execute(ctx, invocation, client, stdout)
}

func parseInvocation(args []string, stderr io.Writer) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, errCommandRequired
	}
	switch commandName(args[0]) {
	case commandStatus, commandNodes, commandComponents:
		return parseReadInvocation(commandName(args[0]), args[1:], stderr)
	case commandComponent:
		return parseComponentInvocation(args[1:], stderr)
	default:
		return invocation{}, fmt.Errorf("parse command %q: %w", args[0], errCommandUnknown)
	}
}

func parseReadInvocation(command commandName, args []string, stderr io.Writer) (invocation, error) {
	parsed := invocation{command: command}
	flags := flag.NewFlagSet(string(command), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&parsed.systemNATSURL, "system-nats-url", "", "System NATS client URL")
	flags.StringVar(&parsed.nodeID, "node-id", "", "Grovlet observer node ID")
	if err := flags.Parse(args); err != nil {
		return invocation{}, fmt.Errorf("parse %s flags: %w", command, err)
	}
	if flags.NArg() != 0 {
		return invocation{}, fmt.Errorf("parse %s: %w: %q", command, errUnexpectedArguments, flags.Args())
	}
	if err := validateConnection(parsed); err != nil {
		return invocation{}, err
	}
	return parsed, nil
}

func parseComponentInvocation(args []string, stderr io.Writer) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, errComponentAction
	}
	parsed := invocation{command: commandComponent, action: componentAction(args[0])}
	if parsed.action != componentStart && parsed.action != componentStop {
		return invocation{}, fmt.Errorf("parse component action %q: %w", args[0], errComponentAction)
	}
	flags := flag.NewFlagSet("component "+string(parsed.action), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&parsed.systemNATSURL, "system-nats-url", "", "System NATS client URL")
	flags.StringVar(&parsed.nodeID, "node-id", "", "target Grovlet node ID")
	serviceID := flags.Uint64("service-id", 0, "stable application service ID")
	if err := flags.Parse(args[1:]); err != nil {
		return invocation{}, fmt.Errorf("parse component %s flags: %w", parsed.action, err)
	}
	if flags.NArg() != 0 {
		return invocation{}, fmt.Errorf("parse component %s: %w: %q", parsed.action, errUnexpectedArguments, flags.Args())
	}
	if err := validateConnection(parsed); err != nil {
		return invocation{}, err
	}
	if *serviceID == 0 || *serviceID > uint64(^grove.ServiceID(0)) {
		return invocation{}, errServiceIDRequired
	}
	parsed.serviceID = grove.ServiceID(*serviceID)
	return parsed, nil
}

func validateConnection(parsed invocation) error {
	if parsed.systemNATSURL == "" {
		return errSystemNATSURLRequired
	}
	if parsed.nodeID == "" {
		return errNodeIDRequired
	}
	return nil
}

func execute(ctx context.Context, parsed invocation, client controlClient, stdout io.Writer) error {
	switch parsed.command {
	case commandStatus:
		return writeStatus(ctx, client, parsed.nodeID, stdout)
	case commandNodes:
		return writeNodes(ctx, client, parsed.nodeID, stdout)
	case commandComponents:
		return writeComponents(ctx, client, parsed.nodeID, stdout)
	case commandComponent:
		return runComponentAction(ctx, client, parsed, stdout)
	default:
		return errCommandUnknown
	}
}

type componentRow struct {
	nodeID string
	status systemnats.ComponentStatus
}

func writeStatus(ctx context.Context, client controlClient, nodeID string, output io.Writer) error {
	cluster, components, err := readCluster(ctx, client, nodeID)
	if err != nil {
		return err
	}
	healthyNodes := 0
	for _, node := range cluster.Nodes {
		if node.Health == systemnats.HealthHealthy {
			healthyNodes++
		}
	}
	healthyComponents := 0
	for _, component := range components {
		if component.status.State == systemnats.ComponentHealthy {
			healthyComponents++
		}
	}
	state := "healthy"
	if !cluster.Ready || healthyNodes != len(cluster.Nodes) || healthyComponents != len(components) {
		state = "degraded"
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintf(writer, "Cluster\t%s\n", state)
	fmt.Fprintf(writer, "Nodes\t%d / %d healthy\n", healthyNodes, len(cluster.Nodes))
	fmt.Fprintf(writer, "Components\t%d / %d healthy\n", healthyComponents, len(components))
	return writer.Flush()
}

func writeNodes(ctx context.Context, client controlClient, nodeID string, output io.Writer) error {
	cluster, err := client.RequestClusterView(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("request cluster view: %w", err)
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "NODE\tHEALTH\tENDPOINT")
	for _, node := range cluster.Nodes {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", node.NodeID, node.Health, node.AdvertisedEndpoint)
	}
	return writer.Flush()
}

func writeComponents(ctx context.Context, client controlClient, nodeID string, output io.Writer) error {
	_, components, err := readCluster(ctx, client, nodeID)
	if err != nil {
		return err
	}
	return writeComponentRows(output, components)
}

func readCluster(ctx context.Context, client controlClient, nodeID string) (systemnats.ClusterView, []componentRow, error) {
	cluster, err := client.RequestClusterView(ctx, nodeID)
	if err != nil {
		return systemnats.ClusterView{}, nil, fmt.Errorf("request cluster view: %w", err)
	}
	rows := make([]componentRow, 0)
	for _, node := range cluster.Nodes {
		view, err := client.RequestComponents(ctx, node.NodeID)
		if err != nil {
			return systemnats.ClusterView{}, nil, fmt.Errorf("request components from %s: %w", node.NodeID, err)
		}
		for _, component := range view.Components {
			rows = append(rows, componentRow{nodeID: node.NodeID, status: component})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].nodeID != rows[j].nodeID {
			return rows[i].nodeID < rows[j].nodeID
		}
		return rows[i].status.ServiceID < rows[j].status.ServiceID
	})
	return cluster, rows, nil
}

func runComponentAction(ctx context.Context, client controlClient, parsed invocation, output io.Writer) error {
	var (
		view systemnats.ComponentView
		err  error
	)
	if parsed.action == componentStart {
		view, err = client.RequestStartComponent(ctx, parsed.nodeID, parsed.serviceID)
	} else {
		view, err = client.RequestStopComponent(ctx, parsed.nodeID, parsed.serviceID)
	}
	if err != nil {
		return fmt.Errorf("%s component %d on %s: %w", parsed.action, parsed.serviceID, parsed.nodeID, err)
	}
	for _, component := range view.Components {
		if component.ServiceID == parsed.serviceID {
			return writeComponentRows(output, []componentRow{{nodeID: parsed.nodeID, status: component}})
		}
	}
	return fmt.Errorf("find component %d on %s: %w", parsed.serviceID, parsed.nodeID, errComponentNotFound)
}

func writeComponentRows(output io.Writer, rows []componentRow) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "NODE\tSERVICE\tNAME\tSTATE\tGENERATION\tERROR")
	for _, row := range rows {
		componentErr := row.status.Error
		if componentErr == "" {
			componentErr = "-"
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			row.nodeID,
			strconv.FormatUint(uint64(row.status.ServiceID), 10),
			row.status.Name,
			row.status.State,
			strconv.FormatUint(row.status.Generation, 10),
			componentErr,
		)
	}
	return writer.Flush()
}
