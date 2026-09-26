package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
)

var (
	// ErrApplicationDefinitionInvalid reports an incomplete or ambiguous
	// application/runtime composition boundary.
	ErrApplicationDefinitionInvalid = errors.New("Grove application definition is invalid")
	// ErrComponentUnknown reports a component identifier that is not owned by
	// the configured application.
	ErrComponentUnknown = errors.New("Grove application component is unknown")
)

// Definition is the explicit composition boundary between an application and
// Grove's runtime. Business services remain ordinary application-owned Go
// types; this definition contributes only runtime wiring.
type Definition struct {
	Name             string
	ApplicationID    string
	EmbeddedArtifact *string
	Configuration    ConfigurationProvider
	Components       []Component
	RegisterActions  func(*console.Registry) error
	IntegrityAction  string
	Scenario         *Scenario
}

// Configuration is an application-owned compiled configuration together with
// the generic artifact metadata Grove needs to identify and place it.
type Configuration struct {
	Value         any
	Revision      string
	Encoding      string
	Payload       []byte
	CanonicalYAML []byte
	Facts         map[string]string
}

// ConfigurationProvider keeps the application schema and validation rules on
// the application side of the runtime boundary.
type ConfigurationProvider struct {
	Default func() (Configuration, error)
	Compile func([]byte) (Configuration, error)
	Decode  func([]byte) (Configuration, error)
	Failure func(error) (field, message string)
}

// Component describes one application-owned service or ingress component.
// Register and HTTPHandler are explicit composition hooks; Grove owns worker
// lifecycle, routing, placement, supervision, and transport.
type Component struct {
	ServiceID   grove.ServiceID
	Name        string
	Kind        string
	Register    func(ComponentContext) error
	HTTPHandler func(ComponentContext) (http.Handler, error)
	// Routes declares the ingress routes served by HTTPHandler so Grove can
	// show them without inspecting application handlers.
	Routes []Route
	// Handlers lists the methods Grove places at handler level. Ordinary
	// methods scale automatically across every node hosting the component;
	// Exclusive ones get a single owner. A component that lists none keeps
	// whole-service placement.
	Handlers []HandlerSpec
}

// HandlerSpec declares one handler. Without Exclusive it scales automatically;
// an Exclusive handler has exactly one active owner of Capability, which its
// workload claims with grove.Exclusive.
type HandlerSpec struct {
	Method grove.MethodID
	// Name labels the handler in Grove's views; it defaults to the method ID.
	Name       string
	Exclusive  bool
	Capability string
}

// Route is one ingress method and path pattern served by a component.
type Route struct {
	Method string
	Path   string
}

// ComponentContext is supplied when Grove starts one application component.
type ComponentContext struct {
	Context       context.Context
	Registry      *grove.Registry
	Client        *grove.Client
	Configuration any
	ConfigDigest  string
	Artifact      ArtifactStatus
	ReadStatus    StatusReader
	ListenAddress string
	Options       []string
}

// Scenario contains optional application-owned probes used by the bundled
// lifecycle demonstration. The runtime stays unaware of request/response
// types and business success semantics.
type Scenario struct {
	NodeCount         int
	DebugNodeCount    int
	InitialPlacements []ScenarioPlacement
	StartupComponents []ScenarioPlacement
	DebugPlacements   []ScenarioPlacement
	RecoveryServiceID grove.ServiceID
	Probe             func(context.Context, string, string) (any, error)
	ProbeHealthy      func(any) bool
	ProbeSummary      func(any) string
	InvalidConfig     func([]byte) (configuration Configuration, field string, message string, err error)
}

// ScenarioPlacement gives a deterministic demo node to an application-owned
// service. It is demonstration composition, not a general scheduler policy.
type ScenarioPlacement struct {
	ServiceID grove.ServiceID
	NodeID    string
	Options   []string
}

var activeApplication Definition

func configureApplication(definition Definition) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	activeApplication = definition
	return nil
}

func validateDefinition(definition Definition) error {
	if definition.Name == "" || definition.ApplicationID == "" {
		return fmt.Errorf("%w: name and application ID are required", ErrApplicationDefinitionInvalid)
	}
	if definition.EmbeddedArtifact == nil || *definition.EmbeddedArtifact == "" {
		return fmt.Errorf("%w: embedded artifact is required", ErrApplicationDefinitionInvalid)
	}
	if definition.Configuration.Default == nil || definition.Configuration.Compile == nil || definition.Configuration.Decode == nil {
		return fmt.Errorf("%w: configuration hooks are required", ErrApplicationDefinitionInvalid)
	}
	if len(definition.Components) == 0 {
		return fmt.Errorf("%w: at least one component is required", ErrApplicationDefinitionInvalid)
	}
	serviceIDs := make(map[grove.ServiceID]struct{}, len(definition.Components))
	kinds := make(map[string]struct{}, len(definition.Components))
	for _, component := range definition.Components {
		if component.ServiceID == 0 || component.Name == "" || component.Kind == "" {
			return fmt.Errorf("%w: component identity is incomplete", ErrApplicationDefinitionInvalid)
		}
		for _, route := range component.Routes {
			if component.HTTPHandler == nil || route.Method == "" || route.Path == "" {
				return fmt.Errorf("%w: component %q has an invalid ingress route", ErrApplicationDefinitionInvalid, component.Name)
			}
		}
		methods := make(map[grove.MethodID]struct{}, len(component.Handlers))
		for _, handler := range component.Handlers {
			_, duplicate := methods[handler.Method]
			if duplicate || (handler.Exclusive && handler.Capability == "") || (!handler.Exclusive && handler.Capability != "") {
				return fmt.Errorf("%w: component %q has an invalid handler declaration", ErrApplicationDefinitionInvalid, component.Name)
			}
			methods[handler.Method] = struct{}{}
		}
		if component.Register == nil && component.HTTPHandler == nil {
			return fmt.Errorf("%w: component %q has no runtime hook", ErrApplicationDefinitionInvalid, component.Name)
		}
		if _, exists := serviceIDs[component.ServiceID]; exists {
			return fmt.Errorf("%w: duplicate service ID %d", ErrApplicationDefinitionInvalid, component.ServiceID)
		}
		if _, exists := kinds[component.Kind]; exists {
			return fmt.Errorf("%w: duplicate component kind %q", ErrApplicationDefinitionInvalid, component.Kind)
		}
		serviceIDs[component.ServiceID] = struct{}{}
		kinds[component.Kind] = struct{}{}
	}
	for _, placement := range append(slices.Clone(definition.scenarioInitialPlacements()), definition.scenarioDebugPlacements()...) {
		if _, ok := serviceIDs[placement.ServiceID]; !ok || placement.NodeID == "" {
			return fmt.Errorf("%w: scenario placement is invalid", ErrApplicationDefinitionInvalid)
		}
	}
	if definition.Scenario != nil {
		if definition.Scenario.NodeCount < 1 || definition.Scenario.DebugNodeCount < 1 {
			return fmt.Errorf("%w: scenario node counts must be positive", ErrApplicationDefinitionInvalid)
		}
		if len(definition.Scenario.InitialPlacements) == 0 || len(definition.Scenario.StartupComponents) == 0 || len(definition.Scenario.DebugPlacements) == 0 {
			return fmt.Errorf("%w: scenario placements are required", ErrApplicationDefinitionInvalid)
		}
		if definition.Scenario.RecoveryServiceID == 0 || definition.Scenario.Probe == nil || definition.Scenario.ProbeHealthy == nil || definition.Scenario.InvalidConfig == nil {
			return fmt.Errorf("%w: scenario hooks are required", ErrApplicationDefinitionInvalid)
		}
		for _, placement := range definition.Scenario.StartupComponents {
			if _, ok := serviceIDs[placement.ServiceID]; !ok || placement.NodeID == "" {
				return fmt.Errorf("%w: startup component is invalid", ErrApplicationDefinitionInvalid)
			}
		}
	}
	return nil
}

func activeApplicationName() string {
	return activeApplication.ApplicationID
}

func (definition Definition) componentByID(serviceID grove.ServiceID) (Component, bool) {
	for _, component := range definition.Components {
		if component.ServiceID == serviceID {
			return component, true
		}
	}
	return Component{}, false
}

func (definition Definition) componentByKind(kind string) (Component, bool) {
	for _, component := range definition.Components {
		if component.Kind == kind {
			return component, true
		}
	}
	return Component{}, false
}

func (definition Definition) scenarioInitialPlacements() []ScenarioPlacement {
	if definition.Scenario == nil {
		return nil
	}
	return definition.Scenario.InitialPlacements
}

func (definition Definition) scenarioDebugPlacements() []ScenarioPlacement {
	if definition.Scenario == nil {
		return nil
	}
	return definition.Scenario.DebugPlacements
}

func (definition Definition) scenarioStartupComponents() []ScenarioPlacement {
	if definition.Scenario == nil {
		return nil
	}
	return definition.Scenario.StartupComponents
}

func (definition Definition) scenarioNodeCount() int {
	if definition.Scenario == nil {
		return 0
	}
	return definition.Scenario.NodeCount
}

func (definition Definition) scenarioDebugNodeCount() int {
	if definition.Scenario == nil {
		return 0
	}
	return definition.Scenario.DebugNodeCount
}

// ClusterStatus is Grove's application-agnostic control-plane read model.
type ClusterStatus struct {
	Health            string            `json:"health"`
	Ready             bool              `json:"ready"`
	Nodes             []NodeStatus      `json:"nodes"`
	Placements        []PlacementStatus `json:"placements"`
	ActiveArtifact    *ArtifactStatus   `json:"active_artifact,omitempty"`
	CandidateArtifact *ArtifactStatus   `json:"candidate_artifact,omitempty"`
	Rollout           *RolloutStatus    `json:"rollout,omitempty"`
}

type NodeStatus struct {
	NodeID     string            `json:"node_id"`
	Health     string            `json:"health"`
	Components []ComponentStatus `json:"components"`
	Error      string            `json:"error,omitempty"`
}

type ComponentStatus struct {
	ServiceID grove.ServiceID `json:"service_id"`
	Name      string          `json:"name"`
	WorkerID  string          `json:"worker_id"`
	State     string          `json:"state"`
	Error     string          `json:"error,omitempty"`
}

type PlacementStatus struct {
	ServiceID         grove.ServiceID `json:"service_id"`
	Name              string          `json:"name"`
	NodeID            string          `json:"node_id"`
	InvocationSubject string          `json:"invocation_subject"`
	ArtifactDigest    string          `json:"artifact_digest"`
	Health            string          `json:"health"`
}

type ArtifactStatus struct {
	ApplicationID  string `json:"application_id"`
	CodeVersion    string `json:"code_version"`
	ArtifactDigest string `json:"artifact_digest"`
	ConfigRevision string `json:"config_revision"`
	ConfigDigest   string `json:"config_digest"`
}

type RolloutStatus struct {
	Generation uint64          `json:"generation"`
	Phase      string          `json:"phase"`
	Failure    *RolloutFailure `json:"failure,omitempty"`
}

type RolloutFailure struct {
	Code      string `json:"code"`
	Component string `json:"component,omitempty"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
}

// StatusReader returns the latest generic Grove control-plane read model.
type StatusReader func(context.Context) (ClusterStatus, error)
