package runtime

import (
	"fmt"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/inspect"
)

// applicationInspection describes the active application to an Inspector,
// with artifact as the active artifact until the cluster records a rollout.
func applicationInspection(artifact ArtifactStatus) inspect.Application {
	return inspect.Application{
		ID:          artifact.ApplicationID,
		Artifact:    artifact,
		ServiceName: applicationServiceName,
	}
}

func applicationServiceName(serviceID grove.ServiceID) string {
	if component, ok := activeApplication.componentByID(serviceID); ok {
		return component.Name
	}
	return fmt.Sprintf("Service %d", serviceID)
}
