package systemnats_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

func TestDebugStreamsRemainIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server, err := systemnats.StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	grovelet, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(grovelet.Close)
	controller := &echoDebugController{closed: make(chan string, 2)}
	if err := grovelet.ServeDebug(ctx, "node-a", controller); err != nil {
		t.Fatal(err)
	}
	client, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	type opened struct {
		target systemnats.DebugTarget
		stream *systemnats.DebugStream
		err    error
	}
	results := make(chan opened, 2)
	for serviceID := range 2 {
		go func() {
			target, stream, err := client.OpenDebug(ctx, "node-a", systemnats.DebugRequest{
				ServiceID: grove.ServiceID(serviceID + 1),
				SessionID: fmt.Sprintf("session-%d", serviceID+1),
			})
			results <- opened{target: target, stream: stream, err: err}
		}()
	}
	streams := make(map[string]*systemnats.DebugStream)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		streams[result.target.WorkerID] = result.stream
	}
	if len(streams) != 2 {
		t.Fatalf("debug targets = %#v; want two workers", streams)
	}
	for workerID, stream := range streams {
		message := []byte("hello " + workerID)
		if _, err := stream.Write(message); err != nil {
			t.Fatal(err)
		}
		response := make([]byte, len(message))
		if _, err := io.ReadFull(stream, response); err != nil {
			t.Fatal(err)
		}
		if string(response) != string(message) {
			t.Errorf("%s response = %q; want %q", workerID, response, message)
		}
	}
	for _, stream := range streams {
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
	}
	closed := map[string]bool{}
	for range 2 {
		select {
		case sessionID := <-controller.closed:
			closed[sessionID] = true
		case <-ctx.Done():
			t.Fatalf("debug streams did not close: %v", closed)
		}
	}
	if !closed["session-1"] || !closed["session-2"] {
		t.Errorf("closed sessions = %v", closed)
	}
}

type echoDebugController struct {
	closed chan string
}

func (c *echoDebugController) OpenDebug(_ context.Context, request systemnats.DebugRequest) (systemnats.DebugTarget, io.ReadWriteCloser, error) {
	client, server := net.Pipe()
	go func() {
		_, _ = io.Copy(server, server)
		_ = server.Close()
	}()
	return systemnats.DebugTarget{
		ServiceID:   request.ServiceID,
		ServiceName: fmt.Sprintf("service-%d", request.ServiceID),
		NodeID:      "node-a",
		WorkerID:    fmt.Sprintf("worker-%d", request.ServiceID),
		ProcessID:   int(request.ServiceID),
	}, closeReporter{ReadWriteCloser: client, close: func() { c.closed <- request.SessionID }}, nil
}

type closeReporter struct {
	io.ReadWriteCloser
	close func()
}

func (s closeReporter) Close() error {
	err := s.ReadWriteCloser.Close()
	s.close()
	return err
}
