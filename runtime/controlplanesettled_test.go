package runtime

import (
	"errors"
	"testing"

	"github.com/grove-project/grove/internal/systemnats"
)

func TestControlPlaneSettledFailsClosedWhileVotersUnknown(t *testing.T) {
	for _, test := range []struct {
		name    string
		voters  int
		nodes   int
		settled bool
	}{
		// A restarted cluster reports no metadata group until it forms; the
		// founder's bootstrap witness then rejoins as a fourth voter.
		{name: "unknown after restart", voters: 0, nodes: 3},
		{name: "witness not released", voters: 4, nodes: 3},
		{name: "one voter per node", voters: 3, nodes: 3, settled: true},
		{name: "fewer voters than nodes", voters: 3, nodes: 4, settled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := controlPlaneSettled(test.voters, test.nodes)
			if test.settled {
				if err != nil {
					t.Fatalf("controlPlaneSettled(%d, %d) = %v; want settled", test.voters, test.nodes, err)
				}
				return
			}
			if !errors.Is(err, systemnats.ErrClusterSettling) {
				t.Fatalf("controlPlaneSettled(%d, %d) = %v; want %v", test.voters, test.nodes, err, systemnats.ErrClusterSettling)
			}
		})
	}
}
