package main

import (
	"slices"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

func TestDesiredStartsSeparatesIntentFromObservedState(t *testing.T) {
	desired := systemnats.DesiredView{Ready: true, Deployments: []systemnats.DesiredDeployment{{
		ApplicationID: "grove-shop", Version: "current", ArtifactDigest: recoveryArtifactDigest,
		Components: []systemnats.DesiredComponent{{ServiceID: 1, NodeID: "node-a"}, {ServiceID: 2, NodeID: "node-b"}},
	}}}
	observed := systemnats.ComponentView{Components: []systemnats.ComponentStatus{
		{ServiceID: 1, State: systemnats.ComponentHealthy},
		{ServiceID: 2, State: systemnats.ComponentFailed},
	}}
	if got := desiredStarts("node-a", desired, observed); len(got) != 0 {
		t.Errorf("healthy assigned starts = %v; want none", got)
	}
	if got := desiredStarts("node-b", desired, observed); !slices.Equal(got, []grove.ServiceID{2}) {
		t.Errorf("failed assigned starts = %v; want [2]", got)
	}
	desired.Ready = false
	if got := desiredStarts("node-b", desired, observed); len(got) != 0 {
		t.Errorf("unready desired starts = %v; want none", got)
	}
}
