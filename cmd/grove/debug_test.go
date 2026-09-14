package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-dap"
	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/systemnats"
)

const debugE2ETimeout = 2 * time.Minute

func TestInjectDAPProcessIDOnlyAdaptsAttach(t *testing.T) {
	initialize := []byte(`{"seq":1,"type":"request","command":"initialize","arguments":{"adapterID":"go"}}`)
	got, err := injectDAPProcessID(initialize, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, initialize) {
		t.Errorf("initialize changed to %s", got)
	}
	attach := []byte(`{"seq":2,"type":"request","command":"attach","arguments":{"mode":"local","processId":999}}`)
	got, err = injectDAPProcessID(attach, 42)
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Arguments struct {
			Mode      string `json:"mode"`
			ProcessID int    `json:"processId"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(got, &message); err != nil {
		t.Fatal(err)
	}
	if message.Arguments.Mode != "local" || message.Arguments.ProcessID != 42 {
		t.Errorf("adapted attach = %s", got)
	}
}

func TestForwardDAPClientPreservesFramingAcrossPartialReads(t *testing.T) {
	initialize := []byte(`{"seq":1,"type":"request","command":"initialize"}`)
	attach := []byte(`{"seq":2,"type":"request","command":"attach","arguments":{}}`)
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n%sContent-Length: %d\r\n\r\n%s", len(initialize), initialize, len(attach), attach)
	reader := &oneByteReader{reader: bytes.NewBufferString(framed)}
	var output bytes.Buffer
	err := forwardDAPClient(&output, reader, 73)
	if err != io.EOF {
		t.Fatalf("forward DAP error = %v; want EOF", err)
	}
	framedReader := bufio.NewReader(&output)
	first, err := readDAPFrame(framedReader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, initialize) {
		t.Errorf("initialize = %s", first)
	}
	second, err := readDAPFrame(framedReader)
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Arguments struct {
			ProcessID int `json:"processId"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(second, &message); err != nil {
		t.Fatal(err)
	}
	if message.Arguments.ProcessID != 73 {
		t.Errorf("attach process ID = %d; want 73", message.Arguments.ProcessID)
	}
}

func TestSelectDebugTargetDiagnosesUnavailableAndAmbiguousServices(t *testing.T) {
	placement := systemnats.PlacementView{Ready: true, Placements: []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "node-1", InvocationSubject: "node-1.orders", ArtifactDigest: "sha256:orders"},
		{ServiceID: 2, NodeID: "node-2", InvocationSubject: "node-2.orders", ArtifactDigest: "sha256:other"},
	}}
	components := map[string]systemnats.ComponentView{
		"node-1": {Components: []systemnats.ComponentStatus{{
			ServiceID: 1, Name: "Orders", InvocationSubject: "node-1.orders",
			State: systemnats.ComponentHealthy, WorkerID: "orders-1", CodeVersion: "v1",
		}}},
		"node-2": {Components: []systemnats.ComponentStatus{{
			ServiceID: 2, Name: "Orders", InvocationSubject: "node-2.orders",
			State: systemnats.ComponentHealthy, WorkerID: "orders-2", CodeVersion: "v1",
		}}},
	}
	if _, err := selectDebugTarget("orders", placement, components); !errors.Is(err, errDebugServiceAmbiguous) {
		t.Errorf("ambiguous service error = %v; want %v", err, errDebugServiceAmbiguous)
	}
	placement.Placements = placement.Placements[:1]
	components["node-1"] = systemnats.ComponentView{Components: []systemnats.ComponentStatus{{
		ServiceID: 1, Name: "Orders", InvocationSubject: "node-1.orders", State: systemnats.ComponentFailed,
	}}}
	if _, err := selectDebugTarget("orders", placement, components); !errors.Is(err, errDebugServiceNotRunning) || !strings.Contains(err.Error(), "failed") {
		t.Errorf("failed service error = %v; want failed state and %v", err, errDebugServiceNotRunning)
	}
	if _, err := selectDebugTarget("payment", placement, components); !errors.Is(err, errDebugServiceNotRunning) {
		t.Errorf("missing service error = %v; want %v", err, errDebugServiceNotRunning)
	}
}

type oneByteReader struct {
	reader *bytes.Buffer
}

func (r *oneByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return r.reader.Read(buffer)
}

// Two real Grove debug commands attach ordinary DAP clients to Orders and
// Payment workers on different Grovlets during one distributed order.
func TestGroveDebugsOrdersAndPaymentWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), debugE2ETimeout)
	defer cancel()
	cluster := startDebugCluster(t, ctx)
	defer stopGrovlets(t, cluster.nodes)
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	components, err := waitForDebugTopology(ctx, transport)
	if err != nil {
		t.Fatalf("wait for five-worker topology: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if components[groveshop.ServiceOrders].WorkerID == components[groveshop.ServicePayment].WorkerID {
		t.Fatalf("Orders and Payment worker IDs are equal: %q", components[groveshop.ServiceOrders].WorkerID)
	}

	debugPorts := reserveRoutePorts(t, 2)
	ordersAddress := "127.0.0.1:" + strconv.Itoa(debugPorts[0])
	paymentAddress := "127.0.0.1:" + strconv.Itoa(debugPorts[1])
	ordersCommand := startDebugCommand(t, ctx, cluster.systemNATSURL, "orders", ordersAddress)
	paymentCommand := startDebugCommand(t, ctx, cluster.systemNATSURL, "payment", paymentAddress)
	defer ordersCommand.stop()
	defer paymentCommand.stop()
	if err := ordersCommand.waitReady(ctx, "Node     node-2", "Worker   orders-1", "DAP listening locally on "+ordersAddress); err != nil {
		t.Fatalf("start Orders debugger: %v\n%s", err, ordersCommand.output.String())
	}
	if err := paymentCommand.waitReady(ctx, "Node     node-4", "Worker   payment-1", "DAP listening locally on "+paymentAddress); err != nil {
		t.Fatalf("start Payment debugger: %v\n%s", err, paymentCommand.output.String())
	}
	if err := waitForDebugStates(ctx, transport, map[grove.ServiceID]systemnats.ComponentState{
		groveshop.ServiceWeb:       systemnats.ComponentHealthy,
		groveshop.ServiceOrders:    systemnats.ComponentDebugging,
		groveshop.ServiceInventory: systemnats.ComponentHealthy,
		groveshop.ServicePayment:   systemnats.ComponentDebugging,
		groveshop.ServiceShipping:  systemnats.ComponentHealthy,
	}); err != nil {
		t.Fatalf("observe independent debug sessions: %v\n%s", err, grovletLogs(cluster.nodes))
	}

	sourcePath, ordersLine, paymentLine := debugBreakpointLocations(t)
	ordersDAP := connectDAPClient(t, ctx, ordersAddress)
	paymentDAP := connectDAPClient(t, ctx, paymentAddress)
	defer ordersDAP.Close()
	defer paymentDAP.Close()
	ordersDAP.initializeAndAttach(t, sourcePath, ordersLine)
	paymentDAP.initializeAndAttach(t, sourcePath, paymentLine)

	orderResult := make(chan struct {
		order groveshop.Order
		err   error
	}, 1)
	go func() {
		order, err := createMVPOrder(ctx, cluster.webAddress, "debug-order")
		orderResult <- struct {
			order groveshop.Order
			err   error
		}{order: order, err: err}
	}()
	ordersThread := ordersDAP.expectBreakpoint(t)
	if got := ordersDAP.evaluate(t, ordersThread, "order.ID"); !strings.Contains(got, "debug-order") {
		t.Errorf("Orders order.ID = %q; want debug-order", got)
	}
	ordersDAP.continueThread(t, ordersThread)
	paymentThread := paymentDAP.expectBreakpoint(t)
	if got := paymentDAP.evaluate(t, paymentThread, "req.OrderID"); !strings.Contains(got, "debug-order") {
		t.Errorf("Payment req.OrderID = %q; want debug-order", got)
	}
	paymentDAP.continueThread(t, paymentThread)
	select {
	case result := <-orderResult:
		wantHistory := []groveshop.OrderStatus{
			groveshop.OrderCreated,
			groveshop.OrderReserved,
			groveshop.OrderPaid,
			groveshop.OrderShipping,
			groveshop.OrderCompleted,
		}
		if result.err != nil || result.order.Status != groveshop.OrderCompleted || !slices.Equal(result.order.History, wantHistory) {
			t.Fatalf("debugged order = %#v, %v; want complete history %v", result.order, result.err, wantHistory)
		}
	case <-ctx.Done():
		t.Fatalf("debugged order did not complete: %v", ctx.Err())
	}
	ordersDAP.disconnect(t)
	paymentDAP.disconnect(t)
	if err := ordersCommand.wait(ctx); err != nil {
		t.Fatalf("Orders debug command: %v\n%s", err, ordersCommand.output.String())
	}
	if err := paymentCommand.wait(ctx); err != nil {
		t.Fatalf("Payment debug command: %v\n%s", err, paymentCommand.output.String())
	}
	if err := waitForDebugStates(ctx, transport, map[grove.ServiceID]systemnats.ComponentState{
		groveshop.ServiceWeb:       systemnats.ComponentHealthy,
		groveshop.ServiceOrders:    systemnats.ComponentHealthy,
		groveshop.ServiceInventory: systemnats.ComponentHealthy,
		groveshop.ServicePayment:   systemnats.ComponentHealthy,
		groveshop.ServiceShipping:  systemnats.ComponentHealthy,
	}); err != nil {
		t.Fatalf("wait for normal supervision: %v\n%s", err, grovletLogs(cluster.nodes))
	}
}

type debugCluster struct {
	nodes         []*grovetest.Node
	systemNATSURL string
	webAddress    string
}

func startDebugCluster(t *testing.T, ctx context.Context) debugCluster {
	t.Helper()
	routePorts := reserveRoutePorts(t, 5)
	webPort := reserveRoutePorts(t, 1)[0]
	cluster := debugCluster{webAddress: "127.0.0.1:" + strconv.Itoa(webPort)}
	extras := [][]string{
		{"--grove-shop-web", "--grove-shop-web-listen", cluster.webAddress},
		{"--grove-shop-orders", "--grove-shop-distributed-orders"},
		{"--grove-shop-inventory"},
		{"--grove-shop-payment"},
		{"--grove-shop-shipping"},
	}
	for i := range extras {
		seed := 0
		if i == 0 {
			seed = 1
		}
		nodeID := fmt.Sprintf("node-%d", i+1)
		args := []string{
			"--node-id", nodeID,
			"--advertise-endpoint", "nats-subject://system/" + nodeID,
			"--system-nats-listen", "127.0.0.1:0",
			"--system-nats-route-listen", "127.0.0.1:" + strconv.Itoa(routePorts[i]),
			"--system-nats-seed", "nats-route://127.0.0.1:" + strconv.Itoa(routePorts[seed]),
			"--system-nats-membership",
			"--system-nats-subject", "_GROVE.system.debug." + nodeID,
			"--delve-path", delvePath,
		}
		node, err := grovetest.StartNode(debugGrovletPath, append(args, extras[i]...)...)
		if err != nil {
			_ = cleanupCommandTestNodes(cluster.nodes)
			t.Fatalf("start %s: %v", nodeID, err)
		}
		cluster.nodes = append(cluster.nodes, node)
	}
	for _, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			_ = cleanupCommandTestNodes(cluster.nodes)
			t.Fatalf("wait for debug Grovlets: %v\n%s", err, grovletLogs(cluster.nodes))
		}
	}
	var err error
	cluster.systemNATSURL, err = commandTestSystemNATSURL(cluster.nodes[0].Logs())
	if err != nil {
		_ = cleanupCommandTestNodes(cluster.nodes)
		t.Fatalf("read debug System NATS URL: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	return cluster
}

func waitForDebugTopology(ctx context.Context, transport *systemnats.Transport) (map[grove.ServiceID]systemnats.ComponentStatus, error) {
	wantNodes := map[grove.ServiceID]string{
		groveshop.ServiceWeb:       "node-1",
		groveshop.ServiceOrders:    "node-2",
		groveshop.ServiceInventory: "node-3",
		groveshop.ServicePayment:   "node-4",
		groveshop.ServiceShipping:  "node-5",
	}
	ticker := time.NewTicker(testConditionInterval)
	defer ticker.Stop()
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		placement, err := transport.RequestPlacement(attemptCtx, "node-1")
		if err == nil {
			lastPlacement = placement
		}
		components := make(map[grove.ServiceID]systemnats.ComponentStatus)
		ready := err == nil && placement.Ready && len(placement.Placements) == len(wantNodes)
		for _, record := range placement.Placements {
			wantNode, expected := wantNodes[record.ServiceID]
			ready = ready && expected && record.NodeID == wantNode
			view, viewErr := transport.RequestComponents(attemptCtx, record.NodeID)
			if viewErr != nil {
				lastErr = errors.Join(lastErr, viewErr)
				ready = false
				continue
			}
			for _, component := range view.Components {
				if component.ServiceID == record.ServiceID && component.InvocationSubject == record.InvocationSubject && component.State == systemnats.ComponentHealthy && component.WorkerID != "" {
					components[record.ServiceID] = component
				}
			}
		}
		cancel()
		if ready && len(components) == len(wantNodes) {
			return components, nil
		}
		lastErr = errors.Join(lastErr, err)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("placement=%#v: %w", lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func waitForDebugStates(ctx context.Context, transport *systemnats.Transport, want map[grove.ServiceID]systemnats.ComponentState) error {
	ticker := time.NewTicker(testConditionInterval)
	defer ticker.Stop()
	last := make(map[grove.ServiceID]systemnats.ComponentState)
	for {
		matches := true
		for serviceID, state := range want {
			nodeID := fmt.Sprintf("node-%d", serviceID)
			if serviceID == groveshop.ServiceWeb {
				nodeID = "node-1"
			} else if serviceID == groveshop.ServiceOrders {
				nodeID = "node-2"
			} else if serviceID == groveshop.ServiceInventory {
				nodeID = "node-3"
			} else if serviceID == groveshop.ServicePayment {
				nodeID = "node-4"
			} else if serviceID == groveshop.ServiceShipping {
				nodeID = "node-5"
			}
			attemptCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := transport.RequestComponents(attemptCtx, nodeID)
			cancel()
			if err != nil {
				matches = false
				continue
			}
			observed := systemnats.ComponentState("")
			for _, component := range view.Components {
				if component.ServiceID == serviceID {
					observed = component.State
					break
				}
			}
			last[serviceID] = observed
			matches = matches && observed == state
		}
		if matches {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("states=%v: %w", last, ctx.Err())
		}
	}
}

type runningDebugCommand struct {
	command  *exec.Cmd
	output   synchronizedBuffer
	done     chan error
	stopOnce sync.Once
}

func startDebugCommand(t *testing.T, ctx context.Context, systemNATSURL, service, listen string) *runningDebugCommand {
	t.Helper()
	running := &runningDebugCommand{done: make(chan error, 1)}
	running.command = exec.CommandContext(
		ctx,
		grovePath,
		"debug",
		"--system-nats-url", systemNATSURL,
		"--node-id", "node-1",
		"--service", service,
		"--listen", listen,
	)
	running.command.Stdout = &running.output
	running.command.Stderr = &running.output
	if err := running.command.Start(); err != nil {
		t.Fatalf("start Grove debug for %s: %v", service, err)
	}
	go func() { running.done <- running.command.Wait() }()
	return running
}

func (c *runningDebugCommand) waitReady(ctx context.Context, fragments ...string) error {
	ticker := time.NewTicker(testConditionInterval)
	defer ticker.Stop()
	for {
		output := c.output.String()
		ready := true
		for _, fragment := range fragments {
			ready = ready && strings.Contains(output, fragment)
		}
		if ready {
			return nil
		}
		select {
		case err := <-c.done:
			return fmt.Errorf("command exited before DAP readiness: %w", err)
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *runningDebugCommand) wait(ctx context.Context) error {
	select {
	case err := <-c.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *runningDebugCommand) stop() {
	c.stopOnce.Do(func() {
		if c.command.Process != nil {
			_ = c.command.Process.Kill()
		}
	})
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func debugBreakpointLocations(t *testing.T) (string, int, int) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller information is unavailable")
	}
	sourcePath := filepath.Clean(filepath.Join(filepath.Dir(testFile), "../../demo/groveshop/groveshop.go"))
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	ordersLine := sourceLine(t, source, "chargeRequest := ChargeRequest{")
	paymentLine := sourceLine(t, source, "if req.AmountCents <= 0 {")
	return sourcePath, ordersLine, paymentLine
}

func sourceLine(t *testing.T, source []byte, marker string) int {
	t.Helper()
	for index, line := range strings.Split(string(source), "\n") {
		if strings.Contains(line, marker) {
			return index + 1
		}
	}
	t.Fatalf("source marker %q not found", marker)
	return 0
}

type testDAPClient struct {
	connection net.Conn
	reader     *bufio.Reader
	sequence   int
}

func connectDAPClient(t *testing.T, ctx context.Context, address string) *testDAPClient {
	t.Helper()
	ticker := time.NewTicker(testConditionInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err == nil {
			return &testDAPClient{connection: connection, reader: bufio.NewReader(connection)}
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("connect to DAP %s: %v", address, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func (c *testDAPClient) Close() error { return c.connection.Close() }

func (c *testDAPClient) request(command string) dap.Request {
	c.sequence++
	return dap.Request{ProtocolMessage: dap.ProtocolMessage{Seq: c.sequence, Type: "request"}, Command: command}
}

func (c *testDAPClient) send(t *testing.T, message dap.Message) {
	t.Helper()
	if err := dap.WriteProtocolMessage(c.connection, message); err != nil {
		t.Fatal(err)
	}
}

func (c *testDAPClient) receive(t *testing.T) dap.Message {
	t.Helper()
	if deadline, ok := t.Context().Deadline(); ok {
		_ = c.connection.SetReadDeadline(deadline)
	}
	message, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		t.Fatal(err)
	}
	if response, ok := message.(interface{ GetResponse() *dap.Response }); ok && !response.GetResponse().Success {
		t.Fatalf("DAP %s failed: %s", response.GetResponse().Command, response.GetResponse().Message)
	}
	return message
}

func (c *testDAPClient) initializeAndAttach(t *testing.T, sourcePath string, line int) {
	t.Helper()
	initialize := &dap.InitializeRequest{
		Request: c.request("initialize"),
		Arguments: dap.InitializeRequestArguments{
			AdapterID:       "go",
			LinesStartAt1:   true,
			ColumnsStartAt1: true,
			PathFormat:      "path",
		},
	}
	c.send(t, initialize)
	for {
		if _, ok := c.receive(t).(*dap.InitializeResponse); ok {
			break
		}
	}
	arguments, err := json.Marshal(map[string]any{"mode": "local", "processId": 0, "stopOnEntry": false})
	if err != nil {
		t.Fatal(err)
	}
	c.send(t, &dap.AttachRequest{Request: c.request("attach"), Arguments: arguments})
	initialized := false
	attached := false
	for !initialized || !attached {
		switch c.receive(t).(type) {
		case *dap.InitializedEvent:
			initialized = true
		case *dap.AttachResponse:
			attached = true
		}
	}
	c.send(t, &dap.SetBreakpointsRequest{
		Request: c.request("setBreakpoints"),
		Arguments: dap.SetBreakpointsArguments{
			Source:      dap.Source{Name: filepath.Base(sourcePath), Path: sourcePath},
			Breakpoints: []dap.SourceBreakpoint{{Line: line}},
		},
	})
	for {
		response, ok := c.receive(t).(*dap.SetBreakpointsResponse)
		if !ok {
			continue
		}
		if len(response.Body.Breakpoints) != 1 || !response.Body.Breakpoints[0].Verified {
			t.Fatalf("DAP breakpoint at %s:%d = %#v", sourcePath, line, response.Body.Breakpoints)
		}
		break
	}
	c.send(t, &dap.ConfigurationDoneRequest{Request: c.request("configurationDone"), Arguments: &dap.ConfigurationDoneArguments{}})
	for {
		if _, ok := c.receive(t).(*dap.ConfigurationDoneResponse); ok {
			break
		}
	}
}

func (c *testDAPClient) expectBreakpoint(t *testing.T) int {
	t.Helper()
	for {
		event, ok := c.receive(t).(*dap.StoppedEvent)
		if ok && event.Body.Reason == "breakpoint" {
			return event.Body.ThreadId
		}
	}
}

func (c *testDAPClient) evaluate(t *testing.T, threadID int, expression string) string {
	t.Helper()
	c.send(t, &dap.StackTraceRequest{
		Request:   c.request("stackTrace"),
		Arguments: dap.StackTraceArguments{ThreadId: threadID, Levels: 1},
	})
	frameID := 0
	for frameID == 0 {
		response, ok := c.receive(t).(*dap.StackTraceResponse)
		if ok {
			if len(response.Body.StackFrames) == 0 {
				t.Fatal("DAP stack trace has no frames")
			}
			frameID = response.Body.StackFrames[0].Id
		}
	}
	c.send(t, &dap.EvaluateRequest{
		Request: c.request("evaluate"),
		Arguments: dap.EvaluateArguments{
			Expression: expression,
			FrameId:    frameID,
			Context:    "watch",
		},
	})
	for {
		if response, ok := c.receive(t).(*dap.EvaluateResponse); ok {
			return response.Body.Result
		}
	}
}

func (c *testDAPClient) continueThread(t *testing.T, threadID int) {
	t.Helper()
	c.send(t, &dap.ContinueRequest{
		Request:   c.request("continue"),
		Arguments: dap.ContinueArguments{ThreadId: threadID},
	})
	for {
		if _, ok := c.receive(t).(*dap.ContinueResponse); ok {
			return
		}
	}
}

func (c *testDAPClient) disconnect(t *testing.T) {
	t.Helper()
	c.send(t, &dap.DisconnectRequest{
		Request:   c.request("disconnect"),
		Arguments: &dap.DisconnectArguments{},
	})
	for {
		if _, ok := c.receive(t).(*dap.DisconnectResponse); ok {
			return
		}
	}
}
