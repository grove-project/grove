package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
	"time"
)

const (
	applicationDiscoveryEnvironment = "GROVE_DISCOVERY_ADDRESS"
	applicationDiscoveryDefault     = "239.255.71.82:41782"
	applicationDiscoveryVersion     = 1
	applicationDiscoveryWindow      = 500 * time.Millisecond
)

var errApplicationDiscoveryInvalid = errors.New("application discovery record is invalid")

// applicationDiscoveryRecord is a network bootstrap hint. It is not
// authoritative membership or cluster state; those remain in System NATS.
type applicationDiscoveryRecord struct {
	ProtocolVersion int                        `json:"protocol_version"`
	Revision        uint64                     `json:"revision"`
	ApplicationID   string                     `json:"application_id"`
	ClusterID       string                     `json:"cluster_id"`
	ArtifactDigest  string                     `json:"artifact_digest"`
	WebAddress      string                     `json:"web_address"`
	NextNode        int                        `json:"next_node"`
	Nodes           []applicationDiscoveryNode `json:"nodes"`
}

type applicationDiscoveryNode struct {
	NodeID        string `json:"node_id"`
	SystemNATSURL string `json:"system_nats_url"`
	RouteURL      string `json:"route_url"`
}

type applicationDiscoveryPacket struct {
	ProtocolVersion int                         `json:"protocol_version"`
	Kind            string                      `json:"kind"`
	Nonce           string                      `json:"nonce,omitempty"`
	ApplicationID   string                      `json:"application_id"`
	ClusterID       string                      `json:"cluster_id"`
	Record          *applicationDiscoveryRecord `json:"record,omitempty"`
}

type applicationDiscovery struct {
	applicationID string
	clusterID     string
	group         *net.UDPAddr
	sourceIP      net.IP
	listener      *net.UDPConn

	mu        sync.RWMutex
	record    *applicationDiscoveryRecord
	closeOnce sync.Once
	done      chan struct{}
}

func newApplicationDiscovery(applicationID, clusterID string) (*applicationDiscovery, error) {
	if applicationID == "" || clusterID == "" {
		return nil, errApplicationDiscoveryInvalid
	}
	address := os.Getenv(applicationDiscoveryEnvironment)
	if address == "" {
		address = applicationDiscoveryDefault
	}
	group, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		return nil, fmt.Errorf("resolve application discovery address: %w", err)
	}
	interface_, sourceIP, err := applicationDiscoveryInterface()
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenMulticastUDP("udp4", interface_, group)
	if err != nil {
		return nil, fmt.Errorf("listen for application discovery: %w", err)
	}
	if err := listener.SetReadBuffer(64 * 1024); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("configure application discovery listener: %w", err)
	}
	discovery := &applicationDiscovery{
		applicationID: applicationID,
		clusterID:     clusterID,
		group:         group,
		sourceIP:      sourceIP,
		listener:      listener,
		done:          make(chan struct{}),
	}
	go discovery.serve()
	return discovery, nil
}

func (d *applicationDiscovery) discover(ctx context.Context) (applicationDiscoveryRecord, bool, error) {
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: d.sourceIP, Port: 0})
	if err != nil {
		return applicationDiscoveryRecord{}, false, fmt.Errorf("open application discovery query: %w", err)
	}
	defer connection.Close()
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return applicationDiscoveryRecord{}, false, fmt.Errorf("create application discovery nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	query := applicationDiscoveryPacket{
		ProtocolVersion: applicationDiscoveryVersion,
		Kind:            "query",
		Nonce:           nonce,
		ApplicationID:   d.applicationID,
		ClusterID:       d.clusterID,
	}
	if err := writeApplicationDiscoveryPacket(connection, d.group, query); err != nil {
		return applicationDiscoveryRecord{}, false, err
	}
	deadline := time.Now().Add(applicationDiscoveryWindow)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetReadDeadline(deadline); err != nil {
		return applicationDiscoveryRecord{}, false, err
	}
	var best applicationDiscoveryRecord
	found := false
	buffer := make([]byte, 64*1024)
	for {
		count, _, err := connection.ReadFromUDP(buffer)
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				if ctx.Err() != nil {
					return applicationDiscoveryRecord{}, false, ctx.Err()
				}
				return best, found, nil
			}
			return applicationDiscoveryRecord{}, false, fmt.Errorf("read application discovery response: %w", err)
		}
		var response applicationDiscoveryPacket
		if json.Unmarshal(buffer[:count], &response) != nil || response.Kind != "response" ||
			response.Nonce != nonce || response.ApplicationID != d.applicationID ||
			response.ClusterID != d.clusterID || response.Record == nil ||
			validateApplicationDiscoveryRecord(*response.Record) != nil {
			continue
		}
		if !found || response.Record.Revision > best.Revision ||
			(response.Record.Revision == best.Revision && len(response.Record.Nodes) > len(best.Nodes)) {
			best = *response.Record
			found = true
		}
	}
}

func (d *applicationDiscovery) publish(record applicationDiscoveryRecord) error {
	if err := validateApplicationDiscoveryRecord(record); err != nil {
		return err
	}
	d.mu.Lock()
	copy := cloneApplicationDiscoveryRecord(record)
	d.record = &copy
	d.mu.Unlock()
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: d.sourceIP, Port: 0})
	if err != nil {
		return fmt.Errorf("open application discovery announcement: %w", err)
	}
	defer connection.Close()
	return writeApplicationDiscoveryPacket(connection, d.group, applicationDiscoveryPacket{
		ProtocolVersion: applicationDiscoveryVersion,
		Kind:            "announce",
		ApplicationID:   d.applicationID,
		ClusterID:       d.clusterID,
		Record:          &record,
	})
}

func applicationDiscoveryInterface() (*net.Interface, net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("list application discovery interfaces: %w", err)
	}
	for _, allowLoopback := range []bool{false, true} {
		for index := range interfaces {
			interface_ := &interfaces[index]
			if interface_.Flags&net.FlagUp == 0 || interface_.Flags&net.FlagMulticast == 0 ||
				(interface_.Flags&net.FlagLoopback != 0) != allowLoopback {
				continue
			}
			addresses, addressErr := interface_.Addrs()
			if addressErr != nil {
				continue
			}
			for _, address := range addresses {
				ip, _, parseErr := net.ParseCIDR(address.String())
				if parseErr == nil && ip.To4() != nil {
					return interface_, ip.To4(), nil
				}
			}
		}
	}
	return nil, nil, errors.New("no multicast-capable IPv4 interface is available for application discovery")
}

func (d *applicationDiscovery) serve() {
	defer close(d.done)
	buffer := make([]byte, 64*1024)
	for {
		count, sender, err := d.listener.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		var packet applicationDiscoveryPacket
		if json.Unmarshal(buffer[:count], &packet) != nil || packet.ProtocolVersion != applicationDiscoveryVersion ||
			packet.ApplicationID != d.applicationID || packet.ClusterID != d.clusterID {
			continue
		}
		switch packet.Kind {
		case "query":
			d.mu.RLock()
			if d.record == nil {
				d.mu.RUnlock()
				continue
			}
			record := cloneApplicationDiscoveryRecord(*d.record)
			d.mu.RUnlock()
			_ = writeApplicationDiscoveryPacket(d.listener, sender, applicationDiscoveryPacket{
				ProtocolVersion: applicationDiscoveryVersion,
				Kind:            "response",
				Nonce:           packet.Nonce,
				ApplicationID:   d.applicationID,
				ClusterID:       d.clusterID,
				Record:          &record,
			})
		case "announce":
			if packet.Record == nil || validateApplicationDiscoveryRecord(*packet.Record) != nil {
				continue
			}
			d.mu.Lock()
			if d.record != nil && (packet.Record.Revision > d.record.Revision ||
				(packet.Record.Revision == d.record.Revision && len(packet.Record.Nodes) > len(d.record.Nodes))) {
				record := cloneApplicationDiscoveryRecord(*packet.Record)
				d.record = &record
			}
			d.mu.Unlock()
		}
	}
}

func writeApplicationDiscoveryPacket(connection *net.UDPConn, destination *net.UDPAddr, packet applicationDiscoveryPacket) error {
	encoded, err := json.Marshal(packet)
	if err != nil {
		return fmt.Errorf("encode application discovery packet: %w", err)
	}
	if _, err := connection.WriteToUDP(encoded, destination); err != nil {
		return fmt.Errorf("send application discovery packet: %w", err)
	}
	return nil
}

func validateApplicationDiscoveryRecord(record applicationDiscoveryRecord) error {
	if record.ProtocolVersion != applicationDiscoveryVersion || record.Revision == 0 || record.ApplicationID == "" ||
		record.ClusterID == "" || record.ArtifactDigest == "" || record.WebAddress == "" ||
		record.NextNode < 2 || len(record.Nodes) == 0 {
		return errApplicationDiscoveryInvalid
	}
	seen := make(map[string]struct{}, len(record.Nodes))
	for _, node := range record.Nodes {
		if node.NodeID == "" || node.SystemNATSURL == "" || node.RouteURL == "" {
			return errApplicationDiscoveryInvalid
		}
		if _, exists := seen[node.NodeID]; exists {
			return errApplicationDiscoveryInvalid
		}
		seen[node.NodeID] = struct{}{}
	}
	return nil
}

func cloneApplicationDiscoveryRecord(record applicationDiscoveryRecord) applicationDiscoveryRecord {
	record.Nodes = slices.Clone(record.Nodes)
	return record
}

func (d *applicationDiscovery) removeNode(_ context.Context, nodeID string) error {
	d.mu.RLock()
	if d.record == nil {
		d.mu.RUnlock()
		return nil
	}
	record := cloneApplicationDiscoveryRecord(*d.record)
	d.mu.RUnlock()
	record.Nodes = slices.DeleteFunc(record.Nodes, func(node applicationDiscoveryNode) bool {
		return node.NodeID == nodeID
	})
	if len(record.Nodes) == 0 {
		d.mu.Lock()
		d.record = nil
		d.mu.Unlock()
		return nil
	}
	record.Revision++
	return d.publish(record)
}

func (d *applicationDiscovery) close() {
	d.closeOnce.Do(func() {
		_ = d.listener.Close()
		<-d.done
	})
}
