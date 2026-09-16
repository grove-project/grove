package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/demo/groveshop"
)

const consoleStateEnvironment = "GROVE_CONSOLE_STATE"

var (
	errConsoleActionRequired = errors.New("application action name is required")
	errConsoleArguments      = errors.New("unexpected application console arguments")
	errConsoleResponse       = errors.New("application console returned an invalid response")
	errConsoleUnavailable    = errors.New("Grove Shop console is not running")
)

type consoleConnection struct {
	SocketPath string `json:"socket_path"`
}

type consoleActionRequest struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

type consoleActionResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Active bool            `json:"active,omitempty"`
}

type consoleActionSession interface {
	InitialResult() any
	Wait(context.Context) error
}

func runApplicationConsole(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("start application console: %w: %q", errConsoleArguments, args)
	}
	runtimeDir, err := os.MkdirTemp("", "groveshop-console-")
	if err != nil {
		return fmt.Errorf("create application console directory: %w", err)
	}
	defer os.RemoveAll(runtimeDir)
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Grove Shop artifact: %w", err)
	}
	controller := newApplicationController(executable, runtimeDir)
	defer controller.close()
	var registry console.Registry
	if err := registerApplicationConsoleActions(&registry, controller); err != nil {
		return err
	}
	tui, err := console.NewTUI(&registry, controller.readModel)
	if err != nil {
		return fmt.Errorf("create Grove Shop TUI: %w", err)
	}
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "actions.sock"))
	if err != nil {
		return fmt.Errorf("listen for application actions: %w", err)
	}
	server := newConsoleActionServer(ctx, listener, &registry)
	defer server.close()
	connection := consoleConnection{SocketPath: listener.Addr().String()}
	if err := writeConsoleConnection(connection); err != nil {
		return err
	}
	defer removeConsoleConnection(connection)

	if err := renderApplicationTUI(ctx, tui, output); err != nil {
		return err
	}
	lines := make(chan string)
	inputErr := make(chan error, 1)
	go scanConsoleInput(input, lines, inputErr)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-inputErr:
			return err
		case line := <-lines:
			action, actionArgs, ok := resolveTUISelection(line)
			if !ok {
				continue
			}
			if action == "q" || action == "quit" {
				return nil
			}
			result, err := tui.Select(ctx, action, actionArgs)
			if err != nil {
				fmt.Fprintf(output, "Error: %v\n", err)
			} else if session, ok := result.(consoleActionSession); ok {
				if err := writeConsoleResult(output, session.InitialResult()); err != nil {
					return err
				}
				if err := session.Wait(ctx); err != nil {
					fmt.Fprintf(output, "Error: %v\n", err)
				}
			} else {
				if err := writeConsoleResult(output, result); err != nil {
					return err
				}
			}
			if err := renderApplicationTUI(ctx, tui, output); err != nil {
				return err
			}
		}
	}
}

func scanConsoleInput(input io.Reader, lines chan<- string, result chan<- error) {
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		lines <- scanner.Text()
	}
	result <- scanner.Err()
}

func resolveTUISelection(line string) (string, []string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil, false
	}
	if len(fields) == 1 && (fields[0] == "q" || fields[0] == "quit") {
		return fields[0], nil, true
	}
	parts := strings.Split(line, ">")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) >= 2 {
		switch {
		case parts[0] == "Cluster" && parts[1] == "Status" && len(parts) == 2:
			return "cluster.status", nil, true
		case parts[0] == "Cluster" && parts[1] == "Restart cluster" && len(parts) == 2:
			return "cluster.restart", nil, true
		case parts[0] == "Deployments" && parts[1] == "New rollout" && len(parts) == 3 && parts[2] != "":
			return "rollout.start", []string{"--config", parts[2]}, true
		case len(parts) == 3 && parts[0] == "Deployments" && parts[1] == "Debug demo" && parts[2] == "Start":
			return "debug.demo.start", nil, true
		case parts[0] == "Application" && parts[1] == "Run resilience scenario" && len(parts) == 2:
			return "resilience.run", nil, true
		case parts[0] == "Application" && parts[1] == "Run integrity check" && len(parts) == 2:
			return groveshop.ActionVerifyOrders, nil, true
		case len(parts) == 6 && parts[0] == "Services" && parts[2] == "Instances" && parts[4] == "Debug" && parts[5] == "Attach":
			return "debug.attach", []string{strings.ToLower(parts[1]), "--listen", "127.0.0.1:0"}, true
		}
	}
	return fields[0], fields[1:], true
}

func renderApplicationTUI(ctx context.Context, tui *console.TUI, output io.Writer) error {
	snapshot, err := tui.Render(ctx)
	if err != nil {
		return fmt.Errorf("render Grove Shop TUI: %w", err)
	}
	if _, err := io.WriteString(output, snapshot+"\nUse Section > Action > argument, or q Quit\n"); err != nil {
		return fmt.Errorf("write Grove Shop TUI: %w", err)
	}
	return nil
}

func runApplicationAction(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errConsoleActionRequired
	}
	connection, err := readConsoleConnection()
	if err != nil {
		return fmt.Errorf("read application console connection: %w", errors.Join(errConsoleUnavailable, err))
	}
	dialer := net.Dialer{}
	stream, err := dialer.DialContext(ctx, "unix", connection.SocketPath)
	if err != nil {
		return fmt.Errorf("connect to application console: %w", errors.Join(errConsoleUnavailable, err))
	}
	defer stream.Close()
	request := consoleActionRequest{Name: args[0], Args: append([]string(nil), args[1:]...)}
	if err := json.NewEncoder(stream).Encode(request); err != nil {
		return fmt.Errorf("send application action: %w", err)
	}
	decoder := json.NewDecoder(stream)
	var response consoleActionResponse
	if err := decoder.Decode(&response); err != nil {
		return fmt.Errorf("receive application action: %w", err)
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	if len(response.Result) == 0 {
		return errConsoleResponse
	}
	if _, err := output.Write(append(response.Result, '\n')); err != nil {
		return fmt.Errorf("write application action result: %w", err)
	}
	if response.Active {
		var final consoleActionResponse
		if err := decoder.Decode(&final); err != nil {
			return fmt.Errorf("wait for application action: %w", err)
		}
		if final.Error != "" {
			return errors.New(final.Error)
		}
	}
	return nil
}

type consoleActionServer struct {
	cancel   context.CancelFunc
	listener net.Listener
	done     chan struct{}
	registry *console.Registry
	wait     sync.WaitGroup
}

func newConsoleActionServer(parent context.Context, listener net.Listener, registry *console.Registry) *consoleActionServer {
	ctx, cancel := context.WithCancel(parent)
	server := &consoleActionServer{
		cancel: cancel, listener: listener, done: make(chan struct{}), registry: registry,
	}
	go server.serve(ctx)
	return server
}

func (s *consoleActionServer) serve(ctx context.Context) {
	defer close(s.done)
	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()
	for {
		stream, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wait.Add(1)
		go func() {
			defer s.wait.Done()
			defer stream.Close()
			s.handle(ctx, stream)
		}()
	}
}

func (s *consoleActionServer) handle(ctx context.Context, stream net.Conn) {
	var request consoleActionRequest
	if err := json.NewDecoder(stream).Decode(&request); err != nil {
		_ = json.NewEncoder(stream).Encode(consoleActionResponse{Error: "decode application action: " + err.Error()})
		return
	}
	result, err := s.registry.Invoke(ctx, request.Name, request.Args)
	if err != nil {
		_ = json.NewEncoder(stream).Encode(consoleActionResponse{Error: err.Error()})
		return
	}
	session, active := result.(consoleActionSession)
	if active {
		result = session.InitialResult()
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		_ = json.NewEncoder(stream).Encode(consoleActionResponse{Error: "encode application action: " + err.Error()})
		return
	}
	encoder := json.NewEncoder(stream)
	if err := encoder.Encode(consoleActionResponse{Result: encoded, Active: active}); err != nil || !active {
		return
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		var buffer [1]byte
		_, _ = stream.Read(buffer[:])
		cancel()
	}()
	if err := session.Wait(sessionCtx); err != nil {
		_ = encoder.Encode(consoleActionResponse{Error: err.Error()})
		return
	}
	_ = encoder.Encode(consoleActionResponse{})
}

func (s *consoleActionServer) close() {
	s.cancel()
	_ = s.listener.Close()
	<-s.done
	s.wait.Wait()
}

func writeConsoleResult(output io.Writer, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode TUI action result: %w", err)
	}
	if _, err := output.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write TUI action result: %w", err)
	}
	return nil
}

func consoleStatePath() string {
	if path := os.Getenv(consoleStateEnvironment); path != "" {
		return path
	}
	return filepath.Join(".grove", "console.json")
}

func writeConsoleConnection(connection consoleConnection) error {
	path := consoleStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create application console state directory: %w", err)
	}
	encoded, err := json.Marshal(connection)
	if err != nil {
		return fmt.Errorf("encode application console connection: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write application console connection %q: %w", path, err)
	}
	return nil
}

func readConsoleConnection() (consoleConnection, error) {
	path := consoleStatePath()
	encoded, err := os.ReadFile(path)
	if err != nil {
		return consoleConnection{}, err
	}
	var connection consoleConnection
	if err := json.Unmarshal(encoded, &connection); err != nil {
		return consoleConnection{}, fmt.Errorf("decode application console connection %q: %w", path, err)
	}
	if connection.SocketPath == "" {
		return consoleConnection{}, errConsoleResponse
	}
	return connection, nil
}

func removeConsoleConnection(connection consoleConnection) {
	current, err := readConsoleConnection()
	if err != nil || current.SocketPath != connection.SocketPath {
		return
	}
	path := consoleStatePath()
	_ = os.Remove(path)
	if os.Getenv(consoleStateEnvironment) == "" {
		_ = os.Remove(filepath.Dir(path))
	}
}
