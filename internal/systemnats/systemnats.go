// Package systemnats hides Grove's System NATS server and request/reply
// transport from application-facing packages.
package systemnats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/nats-io/jwt/v2"
	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	defaultOperationTimeout = 5 * time.Second
	routeCheckInterval      = 10 * time.Millisecond
	systemClusterName       = "grove-system"
	logicalNodeTagPrefix    = "grove-node:"
	systemAccountName       = "$GROVE_SYSTEM"
	systemAccountUser       = "grove-system"
	systemAccountPassword   = "grove-system-internal"
)

var (
	// ErrServerNotReady is returned when an embedded server does not become
	// ready before its startup context ends.
	ErrServerNotReady = errors.New("system NATS server is not ready")
	// ErrSubjectRequired is returned when a transport operation has no subject.
	ErrSubjectRequired = errors.New("system NATS subject is required")
	// ErrHandlerRequired is returned when Serve receives a nil Handler.
	ErrHandlerRequired = errors.New("system NATS handler is required")
	// ErrServerNameRequired is returned when a clustered server has no stable
	// node name.
	ErrServerNameRequired = errors.New("system NATS server name is required")
	// ErrRouteHostRequired is returned when a clustered server has no route
	// listener host.
	ErrRouteHostRequired = errors.New("system NATS route host is required")
	// ErrSeedURLInvalid is returned when a cluster seed is not an absolute NATS
	// route URL.
	ErrSeedURLInvalid = errors.New("system NATS seed URL is invalid")
)

// Error reports a failed hidden System NATS operation.
type Error struct {
	// Operation identifies the server or transport action that failed.
	Operation string
	// Err is the underlying NATS, Grove codec, or context error.
	Err error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %v", e.Operation, e.Err)
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Server is an embedded System NATS server.
type Server struct {
	server       *server.Server
	peer         *server.Server
	peerOptions  *server.Options
	once         sync.Once
	retireMu     sync.Mutex
	peerPrepared bool
}

// StartServer starts an embedded System NATS server on host and port and waits
// until it accepts connections. A zero port selects a free port on host.
func StartServer(ctx context.Context, host string, port int) (*Server, error) {
	return startServer(ctx, &server.Options{
		Host:   host,
		Port:   randomPort(port),
		NoLog:  true,
		NoSigs: true,
	}, false)
}

// ClusterConfig configures one embedded server in a System NATS cluster.
type ClusterConfig struct {
	// Name is the stable name of this NATS server within the cluster.
	Name string
	// Host is the client listener host.
	Host string
	// Port is the client listener port. Zero selects a free port.
	Port int
	// RouteHost is the server-route listener host.
	RouteHost string
	// RoutePort is the server-route listener port. Zero selects a free port.
	RoutePort int
	// SeedURLs are explicit NATS route URLs used to join an existing cluster.
	// An empty slice starts a seed server.
	SeedURLs []string
	// JetStreamStoreDir enables JetStream and stores its state in this directory
	// when non-empty.
	JetStreamStoreDir string
}

// StartClusterServer starts an embedded System NATS cluster server and waits
// until its listeners are ready. A joiner with SeedURLs also waits until at
// least one server route is established.
func StartClusterServer(ctx context.Context, cfg ClusterConfig) (*Server, error) {
	if cfg.Name == "" {
		return nil, &Error{Operation: "configure System NATS cluster", Err: ErrServerNameRequired}
	}
	if cfg.RouteHost == "" {
		return nil, &Error{Operation: "configure System NATS cluster", Err: ErrRouteHostRequired}
	}
	routes, err := parseSeedURLs(cfg.SeedURLs)
	if err != nil {
		return nil, err
	}
	if cfg.JetStreamStoreDir != "" {
		if len(routes) == 0 {
			return startBootstrapClusterServer(ctx, cfg)
		}
		return startJoinedClusterServer(ctx, cfg, routes)
	}
	return startServer(ctx, &server.Options{
		ServerName: cfg.Name,
		Host:       cfg.Host,
		Port:       randomPort(cfg.Port),
		Cluster: server.ClusterOpts{
			Name: systemClusterName,
			Host: cfg.RouteHost,
			Port: randomPort(cfg.RoutePort),
		},
		Routes:    routes,
		JetStream: false,
		NoLog:     true,
		NoSigs:    true,
	}, len(cfg.SeedURLs) != 0)
}

// startBootstrapClusterServer forms the initial JetStream metadata group with
// two embedded peers owned by the same logical Grove node. NATS requires at
// least two peers for a clustered metadata group, including while Grove has a
// single logical node. The shared logical-node tag prevents replicated control
// streams from placing two copies on the same Grove node as the cluster grows.
func startBootstrapClusterServer(ctx context.Context, cfg ClusterConfig) (*Server, error) {
	primaryRoutePort := cfg.RoutePort
	if primaryRoutePort == 0 {
		var err error
		primaryRoutePort, err = reservePort(cfg.RouteHost)
		if err != nil {
			return nil, &Error{Operation: "reserve System NATS bootstrap route", Err: err}
		}
	}
	peerRoutePort, err := reservePort(cfg.RouteHost)
	if err != nil {
		return nil, &Error{Operation: "reserve temporary System NATS peer route", Err: err}
	}
	primaryRoute := &url.URL{
		Scheme: "nats-route",
		Host:   net.JoinHostPort(cfg.RouteHost, fmt.Sprintf("%d", primaryRoutePort)),
	}
	peerRoute := &url.URL{
		Scheme: "nats-route",
		Host:   net.JoinHostPort(cfg.RouteHost, fmt.Sprintf("%d", peerRoutePort)),
	}
	primaryOptions := &server.Options{
		ServerName: cfg.Name,
		Host:       cfg.Host,
		Port:       randomPort(cfg.Port),
		Cluster: server.ClusterOpts{
			Name: systemClusterName,
			Host: cfg.RouteHost,
			Port: primaryRoutePort,
		},
		Routes:             []*url.URL{peerRoute},
		JetStream:          true,
		StoreDir:           cfg.JetStreamStoreDir,
		Tags:               jwt.TagList{logicalNodeTag(cfg.Name)},
		JetStreamUniqueTag: logicalNodeTagPrefix,
		NoLog:              true,
		NoSigs:             true,
	}
	peerOptions := &server.Options{
		ServerName: cfg.Name + "-peer",
		Host:       cfg.Host,
		Port:       server.RANDOM_PORT,
		Cluster: server.ClusterOpts{
			Name: systemClusterName,
			Host: cfg.RouteHost,
			Port: peerRoutePort,
		},
		Routes:             []*url.URL{primaryRoute},
		JetStream:          true,
		StoreDir:           peerStoreDir(cfg.JetStreamStoreDir),
		Tags:               jwt.TagList{logicalNodeTag(cfg.Name)},
		JetStreamUniqueTag: logicalNodeTagPrefix,
		NoLog:              true,
		NoSigs:             true,
	}
	type startResult struct {
		primary bool
		server  *Server
		err     error
	}
	results := make(chan startResult, 2)
	go func() {
		started, err := startServer(ctx, primaryOptions, true)
		results <- startResult{primary: true, server: started, err: err}
	}()
	go func() {
		started, err := startServer(ctx, peerOptions, true)
		results <- startResult{server: started, err: err}
	}()
	var primary, peer *Server
	var startErr error
	for range 2 {
		result := <-results
		if result.err != nil {
			startErr = errors.Join(startErr, result.err)
		} else if result.primary {
			primary = result.server
		} else {
			peer = result.server
		}
	}
	if startErr != nil {
		if primary != nil {
			primary.Shutdown()
		}
		if peer != nil {
			peer.Shutdown()
		}
		return nil, &Error{Operation: "bootstrap System NATS cluster", Err: startErr}
	}
	if err := waitForJetStream(ctx, primary.URL()); err != nil {
		primary.Shutdown()
		peer.Shutdown()
		return nil, &Error{Operation: "wait for bootstrapped System NATS metadata leader", Err: err}
	}
	primary.peer = peer.server
	return primary, nil
}

func startJoinedClusterServer(ctx context.Context, cfg ClusterConfig, routes []*url.URL) (*Server, error) {
	primary, err := startServer(ctx, &server.Options{
		ServerName: cfg.Name,
		Host:       cfg.Host,
		Port:       randomPort(cfg.Port),
		Cluster: server.ClusterOpts{
			Name: systemClusterName,
			Host: cfg.RouteHost,
			Port: randomPort(cfg.RoutePort),
		},
		Routes:             routes,
		JetStream:          true,
		StoreDir:           cfg.JetStreamStoreDir,
		Tags:               jwt.TagList{logicalNodeTag(cfg.Name)},
		JetStreamUniqueTag: logicalNodeTagPrefix,
		NoLog:              true,
		NoSigs:             true,
	}, true)
	if err != nil {
		return nil, err
	}
	if err := waitForJetStream(ctx, primary.URL()); err != nil {
		primary.Shutdown()
		return nil, &Error{Operation: "wait for joined System NATS metadata leader", Err: err}
	}
	primaryRoute, err := url.Parse(primary.RouteURL())
	if err != nil {
		primary.Shutdown()
		return nil, &Error{Operation: "parse joined System NATS route", Err: err}
	}
	primary.peerOptions = &server.Options{
		ServerName: cfg.Name + "-peer",
		Host:       cfg.Host,
		Port:       server.RANDOM_PORT,
		Cluster: server.ClusterOpts{
			Name: systemClusterName,
			Host: cfg.RouteHost,
			Port: server.RANDOM_PORT,
		},
		Routes:             []*url.URL{primaryRoute},
		JetStream:          true,
		StoreDir:           peerStoreDir(cfg.JetStreamStoreDir),
		Tags:               jwt.TagList{logicalNodeTag(cfg.Name)},
		JetStreamUniqueTag: logicalNodeTagPrefix,
		NoLog:              true,
		NoSigs:             true,
	}
	return primary, nil
}

// EnsurePeer adds this logical node's internal metadata witness after its
// primary has registered membership and control-stream replica growth has
// settled. Delaying the witness avoids changing the metadata quorum while the
// same node is performing its initial authoritative writes.
func (s *Server) EnsurePeer(ctx context.Context) error {
	s.retireMu.Lock()
	defer s.retireMu.Unlock()
	if s.peer != nil || s.peerOptions == nil {
		return nil
	}
	peer, err := startServer(ctx, s.peerOptions, true)
	if err != nil {
		return &Error{Operation: "start paired System NATS peer", Err: err}
	}
	if err := waitForJetStreamPeerCurrent(ctx, peer.server); err != nil {
		peer.Shutdown()
		return &Error{Operation: "wait for paired System NATS peer", Err: err}
	}
	s.peer = peer.server
	s.peerOptions = nil
	return nil
}

// HasPeer reports whether this logical node currently owns the cluster's
// internal metadata witness.
func (s *Server) HasPeer() bool {
	s.retireMu.Lock()
	defer s.retireMu.Unlock()
	return s.peer != nil && !s.peerPrepared
}

func waitForJetStreamPeerCurrent(ctx context.Context, natsServer *server.Server) error {
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	for {
		if natsServer.JetStreamEnabled() && natsServer.JetStreamIsCurrent() {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(ErrServerNotReady, ctx.Err())
		}
	}
}

func logicalNodeTag(nodeName string) string {
	return logicalNodeTagPrefix + nodeName
}

func peerStoreDir(storeDir string) string {
	return filepath.Join(filepath.Dir(storeDir), filepath.Base(storeDir)+"-peer")
}

func waitForJetStream(ctx context.Context, serverURL string) error {
	connection, err := nats.Connect(serverURL, nats.Timeout(operationTimeout(ctx)))
	if err != nil {
		return err
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		_, lastErr = js.AccountInfo(attemptCtx)
		cancel()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		}
	}
}

func reservePort(host string) (int, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func parseSeedURLs(seedURLs []string) ([]*url.URL, error) {
	routes := make([]*url.URL, 0, len(seedURLs))
	for _, seed := range seedURLs {
		route, err := url.Parse(seed)
		if err != nil || route.Scheme != "nats-route" || route.Host == "" {
			return nil, &Error{
				Operation: "parse System NATS seed URL",
				Err:       errors.Join(ErrSeedURLInvalid, err),
			}
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func randomPort(port int) int {
	if port == 0 {
		return server.RANDOM_PORT
	}
	return port
}

func startServer(ctx context.Context, opts *server.Options, waitForRoute bool) (*Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "start System NATS server", Err: err}
	}
	configureSystemAccount(opts)
	natsServer, err := server.NewServer(opts)
	if err != nil {
		return nil, &Error{Operation: "create System NATS server", Err: err}
	}
	natsServer.Start()
	startCtx, cancel := operationContext(ctx)
	defer cancel()
	ready := make(chan bool, 1)
	go func() {
		ready <- natsServer.ReadyForConnections(operationTimeout(startCtx))
	}()
	select {
	case isReady := <-ready:
		if isReady {
			if waitForRoute {
				if err := waitForServerRoute(startCtx, natsServer); err != nil {
					shutdownServer(natsServer)
					return nil, &Error{Operation: "join System NATS cluster", Err: err}
				}
			}
			return &Server{server: natsServer}, nil
		}
		shutdownServer(natsServer)
		cause := startCtx.Err()
		if cause == nil {
			cause = ErrServerNotReady
		}
		return nil, &Error{Operation: "start System NATS server", Err: cause}
	case <-startCtx.Done():
		shutdownServer(natsServer)
		return nil, &Error{Operation: "start System NATS server", Err: startCtx.Err()}
	}
}

func configureSystemAccount(opts *server.Options) {
	if !opts.JetStream {
		return
	}
	systemAccount := server.NewAccount(systemAccountName)
	opts.Accounts = []*server.Account{systemAccount}
	opts.SystemAccount = systemAccountName
	opts.Users = []*server.User{{
		Username: systemAccountUser,
		Password: systemAccountPassword,
		Account:  systemAccount,
	}}
}

func waitForServerRoute(ctx context.Context, natsServer *server.Server) error {
	if natsServer.NumRoutes() != 0 {
		return nil
	}
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if natsServer.NumRoutes() != 0 {
				return nil
			}
		case <-ctx.Done():
			return errors.Join(ErrServerNotReady, ctx.Err())
		}
	}
}

func shutdownServer(natsServer *server.Server) {
	natsServer.Shutdown()
	natsServer.WaitForShutdown()
}

// URL returns the client connection URL for the embedded server.
func (s *Server) URL() string {
	return s.server.ClientURL()
}

// RouteURL returns the server-route URL for a clustered embedded server.
// Standalone servers return an empty string.
func (s *Server) RouteURL() string {
	address := s.server.ClusterAddr()
	if address == nil {
		return ""
	}
	return (&url.URL{Scheme: "nats-route", Host: address.String()}).String()
}

// Shutdown closes clients and stops the embedded server. It is safe to call
// Shutdown more than once.
func (s *Server) Shutdown() {
	s.once.Do(func() {
		if s.peer != nil {
			shutdownServer(s.peer)
		}
		shutdownServer(s.server)
	})
}

// PrepareRetire removes the secondary peer from JetStream placement while the
// primary remains available to evacuate this logical node's control replicas.
func (s *Server) PrepareRetire(ctx context.Context) error {
	s.retireMu.Lock()
	defer s.retireMu.Unlock()
	if s.peer == nil || s.peerPrepared {
		return nil
	}
	if err := evacuateControlStreamPeer(ctx, s.URL(), s.peer.Name()); err != nil {
		return &Error{Operation: "evacuate paired System NATS stream peer", Err: err}
	}
	if err := removeMetadataPeer(ctx, s.URL(), s.peer.Name()); err != nil {
		return &Error{Operation: "remove paired System NATS metadata peer", Err: err}
	}
	if s.peer.JetStreamEnabled() {
		if err := s.peer.DisableJetStream(); err != nil {
			return &Error{Operation: "prepare paired System NATS peer retirement", Err: err}
		}
	}
	if err := waitForMetadataPeerEvacuation(ctx, s.URL(), s.peer.Name()); err != nil {
		return &Error{Operation: "wait for paired System NATS stream evacuation", Err: err}
	}
	shutdownServer(s.peer)
	s.peerPrepared = true
	return nil
}

// Retire removes this logical node's primary from the JetStream metadata group
// after control replicas have been evacuated. Abrupt shutdown intentionally
// skips this path to preserve failure evidence and quorum semantics.
func (s *Server) Retire(ctx context.Context) error {
	if err := evacuateControlStreamPeer(ctx, s.URL(), s.server.Name()); err != nil {
		return &Error{Operation: "evacuate primary System NATS stream peer", Err: err}
	}
	if err := removeMetadataPeer(ctx, s.URL(), s.server.Name()); err != nil {
		return &Error{Operation: "remove primary System NATS metadata peer", Err: err}
	}
	if err := s.server.DisableJetStream(); err != nil {
		return &Error{Operation: "retire primary System NATS peer", Err: err}
	}
	if err := waitForMetadataPeerEvacuation(ctx, s.URL(), s.server.Name()); err != nil {
		return &Error{Operation: "wait for primary System NATS stream evacuation", Err: err}
	}
	return nil
}

func evacuateControlStreamPeer(ctx context.Context, serverURL, peer string) error {
	connection, err := nats.Connect(serverURL, nats.Timeout(operationTimeout(ctx)))
	if err != nil {
		return err
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Peer string `json:"peer"`
	}{Peer: peer})
	if err != nil {
		return err
	}
	for _, bucket := range controlStateBuckets {
		operationCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		stream, err := js.Stream(operationCtx, "KV_"+bucket)
		if optionalControlStream(bucket, err) {
			cancel()
			continue
		}
		if err == nil {
			var info *jetstream.StreamInfo
			info, err = stream.Info(operationCtx)
			cancel()
			if err == nil && info.Cluster != nil && info.Cluster.Leader == peer && len(info.Cluster.Replicas) != 0 {
				err = moveControlStreamLeadership(ctx, connection, js, "KV_"+bucket, peer)
			}
			if err == nil && streamClusterContains(info.Cluster, peer) {
				err = removeControlStreamPeer(ctx, connection, js, bucket, peer, payload)
			}
		} else {
			cancel()
		}
		if err != nil {
			return fmt.Errorf("remove %s peer from %s: %w", peer, bucket, err)
		}
		if err := waitForControlStreamPeerEvacuation(ctx, js, bucket, peer); err != nil {
			return err
		}
	}
	return waitForMetadataPeerEvacuation(ctx, serverURL, peer)
}

func removeControlStreamPeer(
	ctx context.Context,
	connection *nats.Conn,
	js jetstream.JetStream,
	bucket string,
	peer string,
	payload []byte,
) error {
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		operationCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		message, err := connection.RequestWithContext(
			operationCtx,
			"$JS.API.STREAM.PEER.REMOVE.KV_"+bucket,
			payload,
		)
		cancel()
		if err == nil {
			var response struct {
				Success bool `json:"success"`
				Error   *struct {
					Code        int    `json:"code"`
					Description string `json:"description"`
				} `json:"error,omitempty"`
			}
			if decodeErr := json.Unmarshal(message.Data, &response); decodeErr != nil {
				lastErr = decodeErr
			} else if response.Success || response.Error != nil && strings.Contains(strings.ToLower(response.Error.Description), "not a member") {
				return nil
			} else if response.Error == nil {
				lastErr = errors.New("stream peer removal was not accepted")
			} else {
				lastErr = fmt.Errorf("API error %d: %s", response.Error.Code, response.Error.Description)
			}
		} else {
			lastErr = err
		}

		// A leader can commit the request before its response reaches the
		// retiring node. Treat the authoritative stream topology as success.
		checkCtx, checkCancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		stream, streamErr := js.Stream(checkCtx, "KV_"+bucket)
		if streamErr == nil {
			var info *jetstream.StreamInfo
			info, streamErr = stream.Info(checkCtx)
			if streamErr == nil && !streamClusterContains(info.Cluster, peer) {
				checkCancel()
				return nil
			}
		}
		checkCancel()
		if streamErr != nil {
			lastErr = errors.Join(lastErr, streamErr)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		}
	}
}

func waitForControlStreamPeerEvacuation(
	ctx context.Context,
	js jetstream.JetStream,
	bucket string,
	peer string,
) error {
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		operationCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		stream, err := js.Stream(operationCtx, "KV_"+bucket)
		if err == nil {
			var info *jetstream.StreamInfo
			info, err = stream.Info(operationCtx)
			if err == nil && streamClusterContains(info.Cluster, peer) {
				err = fmt.Errorf("control stream still includes %s", peer)
			}
			if err == nil && !controlStreamCurrent(info.Cluster) {
				err = errors.New("control stream replicas are not current")
			}
		}
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", bucket, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func moveControlStreamLeadership(
	ctx context.Context,
	connection *nats.Conn,
	js jetstream.JetStream,
	streamName string,
	peer string,
) error {
	requestCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
	message, err := connection.RequestWithContext(
		requestCtx,
		"$JS.API.STREAM.LEADER.STEPDOWN."+streamName,
		nil,
	)
	cancel()
	if err != nil {
		return err
	}
	var response struct {
		Success bool `json:"success"`
		Error   *struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(message.Data, &response); err != nil {
		return err
	}
	if !response.Success {
		if response.Error == nil {
			return errors.New("stream leader step-down was not accepted")
		}
		return fmt.Errorf("API error %d: %s", response.Error.Code, response.Error.Description)
	}
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		operationCtx, operationCancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		stream, err := js.Stream(operationCtx, streamName)
		if err == nil {
			var info *jetstream.StreamInfo
			info, err = stream.Info(operationCtx)
			if err == nil && info.Cluster != nil && info.Cluster.Leader != "" && info.Cluster.Leader != peer {
				operationCancel()
				return nil
			}
		}
		operationCancel()
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		}
	}
}

func waitForMetadataPeerEvacuation(ctx context.Context, serverURL, peer string) error {
	connection, err := nats.Connect(serverURL, nats.Timeout(operationTimeout(ctx)))
	if err != nil {
		return err
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		ready := true
		for _, bucket := range controlStateBuckets {
			attemptCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
			stream, err := js.Stream(attemptCtx, "KV_"+bucket)
			if optionalControlStream(bucket, err) {
				cancel()
				continue
			}
			if err == nil {
				var info *jetstream.StreamInfo
				info, err = stream.Info(attemptCtx)
				if err == nil && streamClusterContains(info.Cluster, peer) {
					err = fmt.Errorf("control stream still includes %s", peer)
				}
				if err == nil && !controlStreamCurrent(info.Cluster) {
					err = errors.New("control stream replicas are not current")
				}
			}
			cancel()
			if err != nil {
				lastErr = fmt.Errorf("%s: %w", bucket, err)
				ready = false
				break
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		}
	}
}

func streamClusterContains(cluster *jetstream.ClusterInfo, peer string) bool {
	if cluster == nil || cluster.Leader == peer {
		return true
	}
	for _, replica := range cluster.Replicas {
		if replica.Name == peer {
			return true
		}
	}
	return false
}

func removeMetadataPeer(ctx context.Context, serverURL, peer string) error {
	connection, err := nats.Connect(
		serverURL,
		nats.UserInfo(systemAccountUser, systemAccountPassword),
		nats.Timeout(operationTimeout(ctx)),
	)
	if err != nil {
		return err
	}
	defer connection.Close()
	payload, err := json.Marshal(struct {
		Peer string `json:"peer"`
	}{Peer: peer})
	if err != nil {
		return err
	}
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		message, requestErr := connection.RequestWithContext(attemptCtx, "$JS.API.SERVER.REMOVE", payload)
		cancel()
		if requestErr == nil {
			var response struct {
				Success bool `json:"success"`
				Error   *struct {
					Code        int    `json:"code"`
					Description string `json:"description"`
				} `json:"error,omitempty"`
			}
			if err := json.Unmarshal(message.Data, &response); err != nil {
				lastErr = err
			} else if response.Success {
				return nil
			} else if response.Error != nil {
				if strings.Contains(strings.ToLower(response.Error.Description), "not a member") {
					return nil
				}
				lastErr = fmt.Errorf("API error %d: %s", response.Error.Code, response.Error.Description)
			}
		} else {
			lastErr = requestErr
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		}
	}
}

// Handler processes one decoded invocation envelope received over System NATS.
type Handler func(context.Context, grove.RequestEnvelope) grove.ResponseEnvelope

// Transport is a hidden System NATS request/reply connection.
type Transport struct {
	connection *nats.Conn
}

// Connect establishes a System NATS transport connection to url.
func Connect(ctx context.Context, url string) (*Transport, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "connect to System NATS", Err: err}
	}
	connection, err := nats.Connect(
		url,
		nats.Name("grove-system"),
		nats.Timeout(operationTimeout(ctx)),
	)
	if err != nil {
		return nil, &Error{Operation: "connect to System NATS", Err: err}
	}
	return &Transport{connection: connection}, nil
}

// Serve registers handler on one System NATS subject and waits until the
// subscription is active.
func (t *Transport) Serve(ctx context.Context, subject string, handler Handler) error {
	if subject == "" {
		return &Error{Operation: "serve System NATS endpoint", Err: ErrSubjectRequired}
	}
	if handler == nil {
		return &Error{Operation: "serve System NATS endpoint", Err: ErrHandlerRequired}
	}
	if _, err := t.connection.Subscribe(subject, func(message *nats.Msg) {
		var request grove.RequestEnvelope
		if err := grove.Decode(message.Data, &request); err != nil {
			respond(message, grove.ResponseEnvelope{
				Error: &grove.ResponseError{Code: grove.ErrorSerialization, Message: err.Error()},
			})
			return
		}
		response := handler(ctx, request)
		response.RequestID = request.RequestID
		respond(message, response)
	}); err != nil {
		return &Error{Operation: "subscribe System NATS endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate System NATS endpoint", Err: err}
	}
	return nil
}

func respond(message *nats.Msg, response grove.ResponseEnvelope) {
	encoded, err := grove.Encode(response)
	if err != nil {
		return
	}
	_ = message.Respond(encoded)
}

// Request sends one invocation envelope to subject and waits for its correlated
// response.
func (t *Transport) Request(
	ctx context.Context,
	subject string,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	if subject == "" {
		return grove.ResponseEnvelope{}, &Error{Operation: "request System NATS endpoint", Err: ErrSubjectRequired}
	}
	encoded, err := grove.Encode(request)
	if err != nil {
		return grove.ResponseEnvelope{}, &Error{Operation: "encode System NATS request", Err: err}
	}
	message, err := t.connection.RequestWithContext(ctx, subject, encoded)
	if err != nil {
		return grove.ResponseEnvelope{}, &Error{Operation: "request System NATS endpoint", Err: err}
	}
	var response grove.ResponseEnvelope
	if err := grove.Decode(message.Data, &response); err != nil {
		return grove.ResponseEnvelope{}, &Error{Operation: "decode System NATS response", Err: err}
	}
	if response.RequestID != request.RequestID {
		return grove.ResponseEnvelope{}, &Error{Operation: "correlate System NATS response", Err: grove.ErrRequestIDMismatch}
	}
	return response, nil
}

// RoutedClient creates a Grove Client that sends every call to subject through
// this System NATS connection.
func (t *Transport) RoutedClient(subject string) (*grove.Client, error) {
	if subject == "" {
		return nil, &Error{Operation: "create routed Grove client", Err: ErrSubjectRequired}
	}
	return grove.NewRoutedClient(subjectRouter{transport: t, subject: subject})
}

type subjectRouter struct {
	transport *Transport
	subject   string
}

func (r subjectRouter) Route(
	ctx context.Context,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	response, err := r.transport.Request(ctx, r.subject, request)
	if err != nil {
		return grove.ResponseEnvelope{}, fmt.Errorf("request: %w: %w", grove.ErrTransportFailure, err)
	}
	return response, nil
}

// Close closes the System NATS connection. It is safe to call Close more than
// once.
func (t *Transport) Close() {
	t.connection.Close()
}

func operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultOperationTimeout)
}

func operationTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			return remaining
		}
		return time.Nanosecond
	}
	return defaultOperationTimeout
}
