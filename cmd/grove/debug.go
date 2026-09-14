package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errDebugServiceNotRunning = errors.New("debug service is not running")
	errDebugServiceAmbiguous  = errors.New("debug service name is ambiguous")
	errDAPFrameInvalid        = errors.New("DAP frame is invalid")
)

func executeDebug(ctx context.Context, parsed invocation, transport *systemnats.Transport, output io.Writer) error {
	target, err := resolveDebugService(ctx, transport, parsed.nodeID, parsed.serviceName)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", parsed.listenAddress)
	if err != nil {
		return fmt.Errorf("listen for local DAP client: %w", err)
	}
	defer listener.Close()
	sessionID, err := newDebugSessionID()
	if err != nil {
		return err
	}
	resolved, stream, err := transport.OpenDebug(ctx, target.NodeID, systemnats.DebugRequest{
		ServiceID: target.ServiceID,
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("start debugger for %s on %s: %w", target.ServiceName, target.NodeID, err)
	}
	defer stream.Close()
	fmt.Fprintf(output, "Service  %s (%d)\n", strings.ToLower(resolved.ServiceName), resolved.ServiceID)
	fmt.Fprintf(output, "Node     %s\n", resolved.NodeID)
	fmt.Fprintf(output, "Worker   %s\n", resolved.WorkerID)
	fmt.Fprintf(output, "Artifact %s\n", resolved.ArtifactDigest)
	fmt.Fprintf(output, "Version  %s\n", resolved.CodeVersion)
	fmt.Fprintf(output, "DAP listening locally on %s\n", listener.Addr())

	client, err := acceptDebugClient(ctx, listener)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("accept local DAP client: %w", err)
	}
	defer client.Close()
	return proxyDAP(client, stream, resolved.ProcessID)
}

func resolveDebugService(ctx context.Context, transport *systemnats.Transport, observerNodeID, serviceName string) (systemnats.DebugTarget, error) {
	placement, err := transport.RequestPlacement(ctx, observerNodeID)
	if err != nil {
		return systemnats.DebugTarget{}, fmt.Errorf("read authoritative service placement: %w", err)
	}
	if !placement.Ready {
		return systemnats.DebugTarget{}, fmt.Errorf("read authoritative service placement: %w", systemnats.ErrPlacementUnavailable)
	}
	componentsByNode := make(map[string]systemnats.ComponentView)
	for _, record := range placement.Placements {
		if _, exists := componentsByNode[record.NodeID]; exists {
			continue
		}
		components, err := transport.RequestComponents(ctx, record.NodeID)
		if err != nil {
			return systemnats.DebugTarget{}, fmt.Errorf("read components on %s: %w", record.NodeID, err)
		}
		componentsByNode[record.NodeID] = components
	}
	return selectDebugTarget(serviceName, placement, componentsByNode)
}

func selectDebugTarget(serviceName string, placement systemnats.PlacementView, componentsByNode map[string]systemnats.ComponentView) (systemnats.DebugTarget, error) {
	var matches []systemnats.DebugTarget
	for _, record := range placement.Placements {
		components := componentsByNode[record.NodeID]
		for _, component := range components.Components {
			if component.ServiceID != record.ServiceID || component.InvocationSubject != record.InvocationSubject || !strings.EqualFold(component.Name, serviceName) {
				continue
			}
			if component.State != systemnats.ComponentHealthy {
				return systemnats.DebugTarget{}, fmt.Errorf("service %s on %s is %s: %w", component.Name, record.NodeID, component.State, errDebugServiceNotRunning)
			}
			matches = append(matches, systemnats.DebugTarget{
				ServiceID:      component.ServiceID,
				ServiceName:    component.Name,
				NodeID:         record.NodeID,
				WorkerID:       component.WorkerID,
				ArtifactDigest: record.ArtifactDigest,
				CodeVersion:    component.CodeVersion,
			})
		}
	}
	if len(matches) == 0 {
		return systemnats.DebugTarget{}, fmt.Errorf("service %q: %w", serviceName, errDebugServiceNotRunning)
	}
	if len(matches) != 1 {
		return systemnats.DebugTarget{}, fmt.Errorf("service %q has %d running targets: %w", serviceName, len(matches), errDebugServiceAmbiguous)
	}
	return matches[0], nil
}

func newDebugSessionID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create debug session identity: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}

func acceptDebugClient(ctx context.Context, listener net.Listener) (net.Conn, error) {
	type result struct {
		connection net.Conn
		err        error
	}
	accepted := make(chan result, 1)
	go func() {
		connection, err := listener.Accept()
		accepted <- result{connection: connection, err: err}
	}()
	select {
	case result := <-accepted:
		return result.connection, result.err
	case <-ctx.Done():
		_ = listener.Close()
		<-accepted
		return nil, ctx.Err()
	}
}

func proxyDAP(client net.Conn, delve io.ReadWriteCloser, processID int) error {
	results := make(chan error, 2)
	go func() {
		_, err := io.Copy(client, delve)
		results <- err
	}()
	go func() {
		results <- forwardDAPClient(delve, client, processID)
	}()
	err := <-results
	_ = client.Close()
	_ = delve.Close()
	secondErr := <-results
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = nil
	}
	if errors.Is(secondErr, io.EOF) || errors.Is(secondErr, net.ErrClosed) {
		secondErr = nil
	}
	return errors.Join(err, secondErr)
}

func forwardDAPClient(destination io.Writer, source io.Reader, processID int) error {
	reader := bufio.NewReader(source)
	for {
		payload, err := readDAPFrame(reader)
		if err != nil {
			return err
		}
		adapted, err := injectDAPProcessID(payload, processID)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(destination, "Content-Length: %d\r\n\r\n", len(adapted)); err != nil {
			return err
		}
		if _, err := destination.Write(adapted); err != nil {
			return err
		}
	}
}

func readDAPFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		name, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(name, "Content-Length") {
			continue
		}
		contentLength, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil || contentLength < 0 {
			return nil, fmt.Errorf("parse DAP Content-Length %q: %w", value, errors.Join(errDAPFrameInvalid, err))
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing DAP Content-Length: %w", errDAPFrameInvalid)
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func injectDAPProcessID(payload []byte, processID int) ([]byte, error) {
	var message struct {
		Type      string         `json:"type"`
		Command   string         `json:"command"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, fmt.Errorf("decode DAP request: %w", err)
	}
	if message.Type != "request" || message.Command != "attach" {
		return payload, nil
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, fmt.Errorf("decode DAP attach request: %w", err)
	}
	arguments, ok := document["arguments"].(map[string]any)
	if !ok {
		arguments = make(map[string]any)
		document["arguments"] = arguments
	}
	arguments["mode"] = "local"
	arguments["processId"] = processID
	adapted, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode DAP attach request: %w", err)
	}
	return adapted, nil
}
