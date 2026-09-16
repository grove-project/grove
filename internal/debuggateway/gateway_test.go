package debuggateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/grove-project/grove/internal/systemnats"
)

func TestInjectDAPProcessID(t *testing.T) {
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

func TestForwardDAPClient(t *testing.T) {
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

func TestSelectTarget(t *testing.T) {
	placement := systemnats.PlacementView{Ready: true, Placements: []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "node-1", InvocationSubject: "node-1.orders", ArtifactDigest: "sha256:orders"},
		{ServiceID: 2, NodeID: "node-2", InvocationSubject: "node-2.orders", ArtifactDigest: "sha256:other"},
	}}
	components := map[string]systemnats.ComponentView{
		"node-1": {Components: []systemnats.ComponentStatus{{ServiceID: 1, Name: "Orders", InvocationSubject: "node-1.orders", State: systemnats.ComponentHealthy, WorkerID: "orders-1", CodeVersion: "v1"}}},
		"node-2": {Components: []systemnats.ComponentStatus{{ServiceID: 2, Name: "Orders", InvocationSubject: "node-2.orders", State: systemnats.ComponentHealthy, WorkerID: "orders-2", CodeVersion: "v1"}}},
	}
	if _, err := selectTarget("orders", placement, components); !errors.Is(err, ErrServiceAmbiguous) {
		t.Errorf("ambiguous service error = %v; want %v", err, ErrServiceAmbiguous)
	}
	placement.Placements = placement.Placements[:1]
	components["node-1"] = systemnats.ComponentView{Components: []systemnats.ComponentStatus{{ServiceID: 1, Name: "Orders", InvocationSubject: "node-1.orders", State: systemnats.ComponentFailed}}}
	if _, err := selectTarget("orders", placement, components); !errors.Is(err, ErrServiceNotRunning) || !strings.Contains(err.Error(), "failed") {
		t.Errorf("failed service error = %v; want failed state and %v", err, ErrServiceNotRunning)
	}
	if _, err := selectTarget("payment", placement, components); !errors.Is(err, ErrServiceNotRunning) {
		t.Errorf("missing service error = %v; want %v", err, ErrServiceNotRunning)
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
