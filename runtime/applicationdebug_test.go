package runtime

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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-dap"
	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

func TestParseDebugDemoArguments(t *testing.T) {
	path, err := parseDebugDemoArguments(nil)
	if err != nil {
		t.Fatal(err)
	}
	if path != "configs/acme.yaml" {
		t.Errorf("default config = %q; want configs/acme.yaml", path)
	}
	path, err = parseDebugDemoArguments([]string{"--config", "customer.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "customer.yaml" {
		t.Errorf("selected config = %q; want customer.yaml", path)
	}
	if _, err := parseDebugDemoArguments([]string{"unexpected"}); !errors.Is(err, errConsoleArguments) {
		t.Errorf("unexpected argument error = %v; want %v", err, errConsoleArguments)
	}
}

func TestDebugApplicationStatusHealthy(t *testing.T) {
	placements := debugApplicationPlacements()
	status := ClusterStatus{
		Ready: true, Health: "healthy",
		ActiveArtifact: &ArtifactStatus{ArtifactDigest: "sha256:debug"},
	}
	for i := 1; i <= activeApplication.scenarioDebugNodeCount(); i++ {
		status.Nodes = append(status.Nodes, NodeStatus{NodeID: "node-" + string(rune('0'+i)), Health: string(systemnats.HealthHealthy)})
	}
	for serviceID, nodeID := range placements {
		status.Placements = append(status.Placements, PlacementStatus{
			ServiceID: serviceID, NodeID: nodeID, Health: string(systemnats.ComponentHealthy),
		})
	}
	if !debugApplicationStatusHealthy(status, "sha256:debug") {
		t.Fatalf("healthy debug application rejected: %#v", status)
	}
	status.Placements[0].NodeID = "wrong-node"
	if debugApplicationStatusHealthy(status, "sha256:debug") {
		t.Fatal("misplaced debug application reported healthy")
	}
}

// runGroveShopDebuggingDemo exercises the documented two-worker Delve/DAP
// sequence. The complete-demo E2E calls it after the rollout lifecycle.
func runGroveShopDebuggingDemo(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	statePath := filepath.Join(t.TempDir(), "console.json")
	consoleCommand := exec.CommandContext(ctx, debugGrovletPath)
	consoleCommand.Env = append(os.Environ(),
		consoleStateEnvironment+"="+statePath,
		"PATH="+filepath.Dir(delvePath)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	input, err := consoleCommand.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var consoleOutput applicationSynchronizedBuffer
	consoleCommand.Stdout = &consoleOutput
	consoleCommand.Stderr = &consoleOutput
	if err := consoleCommand.Start(); err != nil {
		t.Fatal(err)
	}
	waitForConsoleState(t, statePath)
	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		_, _ = io.WriteString(input, "q\n")
		_ = consoleCommand.Wait()
	})

	configPath := filepath.Join("..", "configs", "acme.yaml")
	output := runDebugApplicationAction(t, ctx, statePath, "debug.demo.start", "--config", configPath)
	var result debugDemoActionResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode debug demo result %q: %v", output, err)
	}
	if result.State != "active" || result.WebURL == "" || result.Status.ActiveArtifact == nil ||
		!debugApplicationStatusHealthy(result.Status, result.Status.ActiveArtifact.ArtifactDigest) {
		t.Fatalf("debug demo result = %#v; console=%s", result, consoleOutput.String())
	}
	if applicationPlacementNodeFromStatus(result.Status, groveshop.ServiceOrders) != "node-2" || applicationPlacementNodeFromStatus(result.Status, groveshop.ServicePayment) != "node-4" {
		t.Errorf("debug demo placement = %#v", result.Status.Placements)
	}
	if applicationWorkerFromStatus(result.Status, groveshop.ServiceOrders) != "orders-1" || applicationWorkerFromStatus(result.Status, groveshop.ServicePayment) != "payment-1" {
		t.Errorf("debug demo workers = %#v", result.Status.Nodes)
	}

	debugPorts, err := reserveApplicationPorts(2)
	if err != nil {
		t.Fatal(err)
	}
	ordersAddress := "127.0.0.1:" + strconv.Itoa(debugPorts[0])
	paymentAddress := "127.0.0.1:" + strconv.Itoa(debugPorts[1])
	ordersAction := startDebugApplicationAction(t, ctx, statePath, "orders", ordersAddress)
	paymentAction := startDebugApplicationAction(t, ctx, statePath, "payment", paymentAddress)
	defer ordersAction.stop()
	defer paymentAction.stop()
	if err := ordersAction.waitReady(ctx, `"node_id":"node-2"`, `"worker_id":"orders-1"`, `"dap_endpoint":"`+ordersAddress+`"`); err != nil {
		t.Fatalf("start Orders debugger: %v; output=%s", err, ordersAction.output.String())
	}
	if err := paymentAction.waitReady(ctx, `"node_id":"node-4"`, `"worker_id":"payment-1"`, `"dap_endpoint":"`+paymentAddress+`"`); err != nil {
		t.Fatalf("start Payment debugger: %v; output=%s", err, paymentAction.output.String())
	}
	wantDebugging := map[grove.ServiceID]string{
		groveshop.ServiceWeb:       string(systemnats.ComponentHealthy),
		groveshop.ServiceOrders:    string(systemnats.ComponentDebugging),
		groveshop.ServiceInventory: string(systemnats.ComponentHealthy),
		groveshop.ServicePayment:   string(systemnats.ComponentDebugging),
		groveshop.ServiceShipping:  string(systemnats.ComponentHealthy),
	}
	if _, err := waitForDebugApplicationStates(ctx, statePath, wantDebugging); err != nil {
		t.Fatalf("observe independent debug sessions: %v; console=%s", err, consoleOutput.String())
	}
	logsOutput := runDebugApplicationAction(t, ctx, statePath, "logs.view")
	var activeLogs applicationLogsView
	if err := json.Unmarshal(logsOutput, &activeLogs); err != nil {
		t.Fatalf("decode active debugger logs %q: %v", logsOutput, err)
	}
	if !hasApplicationDebugSession(activeLogs.DebugSessions, "Orders", "node-2", "orders-1", ordersAddress) ||
		!hasApplicationDebugSession(activeLogs.DebugSessions, "Payment", "node-4", "payment-1", paymentAddress) {
		t.Fatalf("active debugger sessions = %#v; want Orders and Payment DAP endpoints", activeLogs.DebugSessions)
	}

	sourcePath, ordersLine, paymentLine := applicationDebugBreakpointLocations(t)
	ordersDAP := connectApplicationDAPClient(t, ctx, ordersAddress)
	paymentDAP := connectApplicationDAPClient(t, ctx, paymentAddress)
	defer ordersDAP.Close()
	defer paymentDAP.Close()
	ordersDAP.initializeAndAttach(t, sourcePath, ordersLine)
	paymentDAP.initializeAndAttach(t, sourcePath, paymentLine)

	orderResult := make(chan struct {
		order groveshop.Order
		err   error
	}, 1)
	go func() {
		order, err := createApplicationOrder(ctx, strings.TrimPrefix(result.WebURL, "http://"), "debug-order")
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
	case completed := <-orderResult:
		if completed.err != nil || !applicationOrderCompleted(completed.order) {
			t.Fatalf("debugged order = %#v, %v; want completed order", completed.order, completed.err)
		}
	case <-ctx.Done():
		t.Fatalf("debugged order did not complete: %v", ctx.Err())
	}

	ordersDAP.disconnect(t)
	paymentDAP.disconnect(t)
	if err := ordersAction.wait(ctx); err != nil {
		t.Fatalf("Orders debug action: %v; output=%s", err, ordersAction.output.String())
	}
	if err := paymentAction.wait(ctx); err != nil {
		t.Fatalf("Payment debug action: %v; output=%s", err, paymentAction.output.String())
	}
	wantHealthy := make(map[grove.ServiceID]string, len(wantDebugging))
	for serviceID := range wantDebugging {
		wantHealthy[serviceID] = string(systemnats.ComponentHealthy)
	}
	finalStatus, err := waitForDebugApplicationStates(ctx, statePath, wantHealthy)
	if err != nil {
		t.Fatalf("wait for normal supervision: %v; console=%s", err, consoleOutput.String())
	}
	if !debugApplicationStatusHealthy(finalStatus, result.Status.ActiveArtifact.ArtifactDigest) {
		t.Fatalf("final debug demo status = %#v; want healthy", finalStatus)
	}
	logsOutput = runDebugApplicationAction(t, ctx, statePath, "logs.view")
	var finalLogs applicationLogsView
	if err := json.Unmarshal(logsOutput, &finalLogs); err != nil {
		t.Fatalf("decode final debugger logs %q: %v", logsOutput, err)
	}
	if len(finalLogs.DebugSessions) != 0 {
		t.Fatalf("active debugger sessions after disconnect = %#v; want none", finalLogs.DebugSessions)
	}
	if _, err := io.WriteString(input, "q\n"); err != nil {
		t.Fatal(err)
	}
	if err := consoleCommand.Wait(); err != nil {
		t.Fatalf("stop debug demo console: %v; output=%s", err, consoleOutput.String())
	}
	stopped = true
	if output := consoleOutput.String(); !strings.Contains(output, "Debug demo > Start") {
		t.Errorf("debug demo TUI action missing: %s", output)
	}
}

func hasApplicationDebugSession(sessions []console.DebugSession, service, node, worker, endpoint string) bool {
	for _, session := range sessions {
		if session.ServiceName == service && session.NodeID == node && session.WorkerID == worker && session.DAPEndpoint == endpoint {
			return true
		}
	}
	return false
}

type runningDebugApplicationAction struct {
	command  *exec.Cmd
	output   applicationSynchronizedBuffer
	done     chan error
	stopOnce sync.Once
}

func startDebugApplicationAction(t *testing.T, ctx context.Context, statePath, service, listen string) *runningDebugApplicationAction {
	t.Helper()
	running := &runningDebugApplicationAction{done: make(chan error, 1)}
	running.command = exec.CommandContext(ctx, debugGrovletPath, "action", "debug.attach", service, "--listen", listen)
	running.command.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
	running.command.Stdout = &running.output
	running.command.Stderr = &running.output
	if err := running.command.Start(); err != nil {
		t.Fatalf("start debug action for %s: %v", service, err)
	}
	go func() { running.done <- running.command.Wait() }()
	return running
}

func (a *runningDebugApplicationAction) waitReady(ctx context.Context, fragments ...string) error {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	for {
		output := a.output.String()
		ready := true
		for _, fragment := range fragments {
			ready = ready && strings.Contains(output, fragment)
		}
		if ready {
			return nil
		}
		select {
		case err := <-a.done:
			return fmt.Errorf("action exited before DAP readiness: %w", err)
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (a *runningDebugApplicationAction) wait(ctx context.Context) error {
	select {
	case err := <-a.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *runningDebugApplicationAction) stop() {
	a.stopOnce.Do(func() {
		if a.command.Process != nil {
			_ = a.command.Process.Kill()
		}
	})
}

type applicationSynchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *applicationSynchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *applicationSynchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func waitForDebugApplicationStates(ctx context.Context, statePath string, want map[grove.ServiceID]string) (ClusterStatus, error) {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last ClusterStatus
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		output, err := runDebugApplicationActionResult(attemptCtx, statePath, "cluster.status")
		cancel()
		if err == nil {
			err = json.Unmarshal(output, &last)
		}
		matches := err == nil
		for serviceID, state := range want {
			matches = matches && applicationComponentState(last, serviceID) == state
		}
		if matches {
			return last, nil
		}
		lastErr = errors.Join(lastErr, err)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return last, fmt.Errorf("status=%#v: %w", last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func applicationComponentState(status ClusterStatus, serviceID grove.ServiceID) string {
	for _, node := range status.Nodes {
		for _, component := range node.Components {
			if component.ServiceID == serviceID {
				return component.State
			}
		}
	}
	return ""
}

func runDebugApplicationActionResult(ctx context.Context, statePath string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, debugGrovletPath, append([]string{"action"}, args...)...)
	command.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
	return command.CombinedOutput()
}

func applicationWorkerFromStatus(status ClusterStatus, serviceID grove.ServiceID) string {
	for _, node := range status.Nodes {
		for _, component := range node.Components {
			if component.ServiceID == serviceID {
				return component.WorkerID
			}
		}
	}
	return ""
}

func runDebugApplicationAction(t *testing.T, ctx context.Context, statePath string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, debugGrovletPath, append([]string{"action"}, args...)...)
	command.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("debug Grove Shop action %q: %v; output=%q", args, err, output)
	}
	return output
}

func applicationDebugBreakpointLocations(t *testing.T) (string, int, int) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller information is unavailable")
	}
	sourcePath := filepath.Clean(filepath.Join(filepath.Dir(testFile), "../internal/testapp/groveshop.go"))
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	return sourcePath,
		applicationDebugSourceLine(t, source, "chargeRequest := ChargeRequest{"),
		applicationDebugSourceLine(t, source, "if req.AmountCents <= 0 {")
}

func applicationDebugSourceLine(t *testing.T, source []byte, marker string) int {
	t.Helper()
	for index, line := range strings.Split(string(source), "\n") {
		if strings.Contains(line, marker) {
			return index + 1
		}
	}
	t.Fatalf("source marker %q not found", marker)
	return 0
}

type applicationDAPClient struct {
	connection net.Conn
	reader     *bufio.Reader
	sequence   int
}

func connectApplicationDAPClient(t *testing.T, ctx context.Context, address string) *applicationDAPClient {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err == nil {
			return &applicationDAPClient{connection: connection, reader: bufio.NewReader(connection)}
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("connect to DAP %s: %v", address, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func (c *applicationDAPClient) Close() error { return c.connection.Close() }

func (c *applicationDAPClient) request(command string) dap.Request {
	c.sequence++
	return dap.Request{ProtocolMessage: dap.ProtocolMessage{Seq: c.sequence, Type: "request"}, Command: command}
}

func (c *applicationDAPClient) send(t *testing.T, message dap.Message) {
	t.Helper()
	if err := dap.WriteProtocolMessage(c.connection, message); err != nil {
		t.Fatal(err)
	}
}

func (c *applicationDAPClient) receive(t *testing.T) dap.Message {
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

func (c *applicationDAPClient) initializeAndAttach(t *testing.T, sourcePath string, line int) {
	t.Helper()
	c.send(t, &dap.InitializeRequest{
		Request: c.request("initialize"),
		Arguments: dap.InitializeRequestArguments{
			AdapterID: "go", LinesStartAt1: true, ColumnsStartAt1: true, PathFormat: "path",
		},
	})
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
			return
		}
	}
}

func (c *applicationDAPClient) expectBreakpoint(t *testing.T) int {
	t.Helper()
	for {
		event, ok := c.receive(t).(*dap.StoppedEvent)
		if ok && event.Body.Reason == "breakpoint" {
			return event.Body.ThreadId
		}
	}
}

func (c *applicationDAPClient) evaluate(t *testing.T, threadID int, expression string) string {
	t.Helper()
	c.send(t, &dap.StackTraceRequest{
		Request: c.request("stackTrace"), Arguments: dap.StackTraceArguments{ThreadId: threadID, Levels: 1},
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
		Request:   c.request("evaluate"),
		Arguments: dap.EvaluateArguments{Expression: expression, FrameId: frameID, Context: "watch"},
	})
	for {
		if response, ok := c.receive(t).(*dap.EvaluateResponse); ok {
			return response.Body.Result
		}
	}
}

func (c *applicationDAPClient) continueThread(t *testing.T, threadID int) {
	t.Helper()
	c.send(t, &dap.ContinueRequest{
		Request: c.request("continue"), Arguments: dap.ContinueArguments{ThreadId: threadID},
	})
	for {
		if _, ok := c.receive(t).(*dap.ContinueResponse); ok {
			return
		}
	}
}

func (c *applicationDAPClient) disconnect(t *testing.T) {
	t.Helper()
	c.send(t, &dap.DisconnectRequest{
		Request: c.request("disconnect"), Arguments: &dap.DisconnectArguments{},
	})
	for {
		if _, ok := c.receive(t).(*dap.DisconnectResponse); ok {
			return
		}
	}
}
