package systemnats

import (
	"time"
	_ "unsafe" // for go:linkname
)

// The embedded NATS server keeps its Raft timing in unexported package
// variables that server.Options cannot reach. They are read when a Raft group
// starts, so they must be set before the first embedded server is created.
// The linknames are pinned to the nats-server version in go.mod; a version
// that renames them fails at link time rather than silently.

//go:linkname natsMinElectionTimeout github.com/nats-io/nats-server/v2/server.minElectionTimeout
var natsMinElectionTimeout time.Duration

//go:linkname natsMaxElectionTimeout github.com/nats-io/nats-server/v2/server.maxElectionTimeout
var natsMaxElectionTimeout time.Duration

//go:linkname natsHeartbeatInterval github.com/nats-io/nats-server/v2/server.hbInterval
var natsHeartbeatInterval time.Duration

//go:linkname natsLostQuorumInterval github.com/nats-io/nats-server/v2/server.lostQuorumInterval
var natsLostQuorumInterval time.Duration

//go:linkname natsLostQuorumCheck github.com/nats-io/nats-server/v2/server.lostQuorumCheck
var natsLostQuorumCheck time.Duration

func init() {
	natsMinElectionTimeout = 1 * time.Second
	natsMaxElectionTimeout = 2500 * time.Millisecond
	natsHeartbeatInterval = 250 * time.Millisecond
	natsLostQuorumInterval = 2500 * time.Millisecond
	natsLostQuorumCheck = 2500 * time.Millisecond
}
