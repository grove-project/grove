package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
)

const (
	delveStartupTimeout  = 5 * time.Second
	delveShutdownTimeout = 2 * time.Second
	delveDialInterval    = 20 * time.Millisecond
)

var errDelveUnavailable = errors.New("Delve executable is unavailable")

type debugController struct {
	nodeID     string
	runtimeDir string
	delvePath  string
	components *componentManager
}

func newDebugController(nodeID, runtimeDir, delvePath string, components *componentManager) *debugController {
	return &debugController{
		nodeID:     nodeID,
		runtimeDir: runtimeDir,
		delvePath:  delvePath,
		components: components,
	}
}

func (c *debugController) OpenDebug(ctx context.Context, request systemnats.DebugRequest) (systemnats.DebugTarget, io.ReadWriteCloser, error) {
	target, err := c.components.beginDebug(request.ServiceID)
	if err != nil {
		return systemnats.DebugTarget{}, nil, fmt.Errorf("select service %d worker on %s: %w", request.ServiceID, c.nodeID, err)
	}
	endDebug := func() { c.components.endDebug(request.ServiceID, target.generation) }
	delvePath := c.delvePath
	if delvePath == "" {
		delvePath, err = exec.LookPath("dlv")
		if err != nil {
			endDebug()
			return systemnats.DebugTarget{}, nil, fmt.Errorf("locate Delve (install dlv or configure --delve-path): %w", errors.Join(errDelveUnavailable, err))
		}
	}
	socketPath := filepath.Join(c.runtimeDir, debugSocketName(request.SessionID))
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		endDebug()
		return systemnats.DebugTarget{}, nil, fmt.Errorf("remove stale Delve socket: %w", err)
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	var logs bytes.Buffer
	command := exec.CommandContext(sessionCtx, delvePath, "dap", "--listen=unix:"+socketPath)
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		cancel()
		endDebug()
		return systemnats.DebugTarget{}, nil, fmt.Errorf("start Delve for %s: %w", target.workerID, err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	startupCtx, stopStartup := context.WithTimeout(ctx, delveStartupTimeout)
	defer stopStartup()
	ticker := time.NewTicker(delveDialInterval)
	defer ticker.Stop()
	for {
		connection, dialErr := net.Dial("unix", socketPath)
		if dialErr == nil {
			return systemnats.DebugTarget{
				ServiceID:      target.serviceID,
				ServiceName:    target.serviceName,
				NodeID:         c.nodeID,
				WorkerID:       target.workerID,
				ArtifactDigest: target.artifactDigest,
				CodeVersion:    target.codeVersion,
				ProcessID:      target.processID,
			}, &delveStream{
				connection: connection,
				command:    command,
				done:       done,
				cancel:     cancel,
				socketPath: socketPath,
				endDebug:   endDebug,
				worker:     target.process,
				workerID:   target.workerID,
			}, nil
		}
		select {
		case commandErr := <-done:
			cancel()
			endDebug()
			_ = os.Remove(socketPath)
			return systemnats.DebugTarget{}, nil, fmt.Errorf("Delve exited before accepting DAP for %s: %w: %s", target.workerID, commandErr, strings.TrimSpace(logs.String()))
		case <-ticker.C:
		case <-startupCtx.Done():
			cancel()
			commandErr := <-done
			endDebug()
			_ = os.Remove(socketPath)
			return systemnats.DebugTarget{}, nil, fmt.Errorf("wait for Delve DAP for %s: %w: %v: %s", target.workerID, startupCtx.Err(), commandErr, strings.TrimSpace(logs.String()))
		}
	}
}

func debugSocketName(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return "debug-" + hex.EncodeToString(digest[:8]) + ".sock"
}

type delveStream struct {
	connection net.Conn
	command    *exec.Cmd
	done       chan error
	cancel     context.CancelFunc
	socketPath string
	endDebug   func()
	worker     componentProcess
	workerID   string
	closeOnce  sync.Once
	closeErr   error
}

func (s *delveStream) Read(buffer []byte) (int, error) {
	count, err := s.connection.Read(buffer)
	if err == nil {
		return count, nil
	}
	select {
	case <-s.worker.Done():
		workerErr := s.worker.Err()
		if workerErr == nil {
			workerErr = errors.New("worker exited")
		}
		return count, fmt.Errorf("debugged worker %s exited: %w", s.workerID, workerErr)
	default:
		return count, err
	}
}

func (s *delveStream) Write(data []byte) (int, error) {
	return s.connection.Write(data)
}

func (s *delveStream) Close() error {
	s.closeOnce.Do(func() {
		connectionErr := s.connection.Close()
		timer := time.NewTimer(delveShutdownTimeout)
		select {
		case commandErr := <-s.done:
			s.closeErr = errors.Join(connectionErr, commandErr)
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			s.cancel()
			if err := s.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				s.closeErr = errors.Join(connectionErr, err)
			}
			<-s.done
		}
		s.cancel()
		if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.closeErr = errors.Join(s.closeErr, err)
		}
		s.endDebug()
	})
	return s.closeErr
}
