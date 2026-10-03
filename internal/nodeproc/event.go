package nodeproc

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// Lifecycle event names a Grovlet writes to stdout.
const (
	// EventReady reports that the node finished starting and serves.
	EventReady = "ready"
	// EventStopped reports that the node finished a graceful shutdown.
	EventStopped = "stopped"
)

// ErrReadyEventMissing is returned by ReadyEvent when the output holds no
// ready event that carries a System NATS URL.
var ErrReadyEventMissing = errors.New("ready lifecycle event is missing")

// Event is one line of a Grovlet's machine-readable lifecycle stream. The
// Grovlet writes one JSON-encoded Event per line to stdout; supervisors read
// readiness, shutdown and control-plane changes from that stream.
type Event struct {
	Event              string `json:"event"`
	NodeID             string `json:"node_id,omitempty"`
	AdvertisedEndpoint string `json:"advertised_endpoint,omitempty"`
	SystemNATSURL      string `json:"system_nats_url,omitempty"`
	SystemNATSRouteURL string `json:"system_nats_route_url,omitempty"`
	ConfigRevision     string `json:"config_revision,omitempty"`
	ConfigDigest       string `json:"config_digest,omitempty"`
	ArtifactDigest     string `json:"artifact_digest,omitempty"`
	ClusterName        string `json:"cluster_name,omitempty"`
	NodeZone           string `json:"node_zone,omitempty"`
	// Leader, Voters and Detail describe control-plane events such as
	// metadata leader elections and cluster formation.
	Leader string `json:"leader,omitempty"`
	Voters int    `json:"voters,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ReadyEvent returns the latest ready event in captured node output that
// carries a System NATS URL. Output from several process lifetimes may be
// concatenated, so the latest lifetime wins. Lines that are not lifecycle
// events, such as stderr output, are skipped.
func ReadyEvent(output string) (Event, error) {
	var ready Event
	found := false
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Event == EventReady && event.SystemNATSURL != "" {
			ready = event
			found = true
		}
	}
	if !found {
		return Event{}, ErrReadyEventMissing
	}
	return ready, nil
}

// parseEvent decodes one stdout line of the lifecycle stream.
func parseEvent(line []byte) (Event, error) {
	var event Event
	err := json.Unmarshal(bytes.TrimSpace(line), &event)
	return event, err
}
