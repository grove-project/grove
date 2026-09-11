package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	workerOrders    = "orders"
	workerInventory = "inventory"
	workerWeb       = "web"
)

type workerConfig struct {
	component       string
	systemNATSURL   string
	subject         string
	placementNodeID string
	parentFD        int
	webListen       string
}

type workerProcess struct {
	cmd       *exec.Cmd
	keepalive *os.File
	done      chan struct{}
	mu        sync.Mutex
	err       error
}

func newWorkerStarter(systemNATSURL, placementNodeID string) componentStarter {
	return func(ctx context.Context, spec componentSpec) (componentProcess, error) {
		return startWorkerProcess(ctx, systemNATSURL, placementNodeID, spec)
	}
}

func startWorkerProcess(ctx context.Context, systemNATSURL, placementNodeID string, spec componentSpec) (*workerProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Grovlet executable: %w", err)
	}
	parentRead, parentWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create worker parent pipe: %w", err)
	}
	args := []string{
		"worker",
		"--component", spec.kind,
		"--system-nats-url", systemNATSURL,
		"--subject", spec.subject,
		"--placement-node-id", placementNodeID,
		"--parent-fd", "3",
	}
	args = append(args, spec.workerArgs...)
	cmd := exec.Command(executable, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		parentRead.Close()
		parentWrite.Close()
		return nil, fmt.Errorf("open worker stdout: %w", err)
	}
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{parentRead}
	if err := cmd.Start(); err != nil {
		parentRead.Close()
		parentWrite.Close()
		return nil, fmt.Errorf("start worker: %w", err)
	}
	parentRead.Close()
	process := &workerProcess{cmd: cmd, keepalive: parentWrite, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()

	ready := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(stdout)
		for {
			var event lifecycleEvent
			if err := decoder.Decode(&event); err != nil {
				ready <- err
				return
			}
			if event.Event == "ready" {
				ready <- nil
				return
			}
		}
	}()
	startCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		startCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	}
	defer cancel()
	select {
	case err := <-ready:
		if err != nil {
			_ = process.Stop(startCtx)
			return nil, fmt.Errorf("read worker readiness: %w", err)
		}
		return process, nil
	case <-process.done:
		exitErr := process.Err()
		if exitErr == nil {
			exitErr = errors.New("worker exited without reporting readiness")
		}
		return nil, fmt.Errorf("worker exited before readiness: %w", exitErr)
	case <-startCtx.Done():
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		_ = process.Stop(stopCtx)
		stopCancel()
		return nil, fmt.Errorf("wait for worker readiness: %w", startCtx.Err())
	}
}

func (p *workerProcess) Stop(ctx context.Context) error {
	_ = p.keepalive.Close()
	select {
	case <-p.done:
		return p.Err()
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		<-p.done
		return ctx.Err()
	}
}

func (p *workerProcess) Kill(ctx context.Context) error {
	_ = p.keepalive.Close()
	if err := p.cmd.Process.Kill(); err != nil {
		return err
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *workerProcess) Done() <-chan struct{} {
	return p.done
}

func (p *workerProcess) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func runWorker(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseWorkerConfig(args, stderr)
	if err != nil {
		return err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	parent := os.NewFile(uintptr(cfg.parentFD), "grovlet-parent")
	if parent == nil {
		return errors.New("worker parent pipe is unavailable")
	}
	defer parent.Close()
	go func() {
		_, _ = io.Copy(io.Discard, parent)
		cancel()
	}()

	transport, err := systemnats.Connect(workerCtx, cfg.systemNATSURL)
	if err != nil {
		return err
	}
	defer transport.Close()
	registry := &grove.Registry{}
	var (
		webServer *http.Server
		webDone   chan error
	)
	switch cfg.component {
	case workerOrders:
		client, err := transport.ObservedPlacementClient(cfg.placementNodeID)
		if err != nil {
			return err
		}
		orders := groveshop.NewGroveOrders(client, &groveshop.Payment{}, &groveshop.Shipping{})
		if err := groveshop.RegisterOrders(registry, orders); err != nil {
			return fmt.Errorf("register Grove Shop Orders: %w", err)
		}
	case workerInventory:
		if err := groveshop.RegisterInventory(registry, &groveshop.Inventory{}); err != nil {
			return fmt.Errorf("register Grove Shop Inventory: %w", err)
		}
	case workerWeb:
		listener, err := net.Listen("tcp", cfg.webListen)
		if err != nil {
			return fmt.Errorf("listen for Grove Shop Web: %w", err)
		}
		webServer = &http.Server{
			Handler:           groveshop.WebHandler(),
			ReadHeaderTimeout: 5 * time.Second,
		}
		defer webServer.Close()
		webDone = make(chan error, 1)
		go func() { webDone <- webServer.Serve(listener) }()
	default:
		return fmt.Errorf("unknown worker component %q", cfg.component)
	}
	dispatcher, err := grove.NewDispatcher(registry)
	if err != nil {
		return err
	}
	if err := transport.Serve(workerCtx, cfg.subject, dispatcher.Dispatch); err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	if err := encoder.Encode(lifecycleEvent{Event: "ready"}); err != nil {
		return fmt.Errorf("encode worker ready event: %w", err)
	}
	select {
	case <-workerCtx.Done():
	case err := <-webDone:
		return fmt.Errorf("serve Grove Shop Web: %w", err)
	}
	if webServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		err := webServer.Shutdown(shutdownCtx)
		shutdownCancel()
		if err != nil {
			return fmt.Errorf("stop Grove Shop Web: %w", err)
		}
		if err := <-webDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve Grove Shop Web: %w", err)
		}
	}
	if err := encoder.Encode(lifecycleEvent{Event: "stopped"}); err != nil {
		return fmt.Errorf("encode worker stopped event: %w", err)
	}
	return nil
}

func parseWorkerConfig(args []string, stderr io.Writer) (workerConfig, error) {
	var cfg workerConfig
	flags := flag.NewFlagSet("grovlet worker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.component, "component", "", "Grove Shop component kind")
	flags.StringVar(&cfg.systemNATSURL, "system-nats-url", "", "parent Grovlet System NATS URL")
	flags.StringVar(&cfg.subject, "subject", "", "component invocation subject")
	flags.StringVar(&cfg.placementNodeID, "placement-node-id", "", "parent placement observer node ID")
	flags.IntVar(&cfg.parentFD, "parent-fd", 0, "parent-lifetime file descriptor")
	flags.StringVar(&cfg.webListen, "web-listen", "", "Grove Shop Web HTTP listen address")
	if err := flags.Parse(args); err != nil {
		return workerConfig{}, fmt.Errorf("parse worker flags: %w", err)
	}
	if flags.NArg() != 0 || cfg.component == "" || cfg.systemNATSURL == "" || cfg.subject == "" || cfg.placementNodeID == "" || cfg.parentFD < 3 {
		return workerConfig{}, errors.New("worker configuration is incomplete")
	}
	if (cfg.component == workerWeb) != (cfg.webListen != "") {
		return workerConfig{}, errors.New("Web worker listen configuration is invalid")
	}
	return cfg, nil
}

func componentInvocationSubject(nodeSubject string, serviceID grove.ServiceID) string {
	return nodeSubject + ".service." + strconv.FormatUint(uint64(serviceID), 10)
}
