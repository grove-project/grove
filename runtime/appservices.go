package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/consoleview"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	placementHealthy  = consoleview.PlacementHealthy
	placementLost     = consoleview.PlacementLost
	placementStarting = consoleview.PlacementStarting

	handlerScalingAutomatic = "automatic"
	handlerScalingExclusive = "exclusive"
	handlerScalingService   = "service"
)

// The services view's rows are console view models (internal/consoleview),
// which interactive frontends render.
type (
	applicationPlacementRow = consoleview.Placement
	applicationExecution    = consoleview.Execution
	applicationProcessRow   = consoleview.Process
	applicationHandlerRow   = consoleview.Handler
	applicationServiceRow   = consoleview.Service
	applicationHostedRow    = consoleview.Hosted
	applicationNodeRow      = consoleview.Node
	applicationServicesView = consoleview.Services
)

type handlerKey struct {
	service grove.ServiceID
	method  grove.MethodID
}

// buildServicesView derives the App -> Services -> Handler -> Placement model
// and its node -> placement inverse from what the runtime observes: the
// resolved handler placements, lost placements, registrations and node health.
// It adds no state of its own, so it always agrees with routing.
func buildServicesView(handlers systemnats.HandlerPlacementView, status ClusterStatus) applicationServicesView {
	health := make(map[string]string, len(status.Nodes))
	for _, node := range status.Nodes {
		health[node.NodeID] = node.Health
	}
	live := func(nodeID string) bool {
		state, known := health[nodeID]
		return !known || state == string(systemnats.HealthHealthy)
	}
	type nodeService struct {
		nodeID  string
		service grove.ServiceID
	}
	executions := map[nodeService]applicationExecution{}
	for _, node := range status.Nodes {
		for _, component := range node.Components {
			executions[nodeService{node.NodeID, component.ServiceID}] = applicationExecution{
				ExecutionMode: component.ExecutionMode, ProcessID: component.ProcessID, PID: component.PID,
			}
		}
	}

	type facts struct {
		spec      *HandlerSpec
		placement *systemnats.HandlerPlacement
		lost      map[string]bool
		register  map[string]bool
		exclusive bool
	}
	byKey := map[handlerKey]*facts{}
	get := func(service grove.ServiceID, method grove.MethodID) *facts {
		key := handlerKey{service, method}
		if byKey[key] == nil {
			byKey[key] = &facts{lost: map[string]bool{}, register: map[string]bool{}}
		}
		return byKey[key]
	}
	for _, component := range activeApplication.Components {
		for i := range component.Handlers {
			spec := component.Handlers[i]
			f := get(component.ServiceID, spec.Method)
			f.spec, f.exclusive = &spec, spec.Exclusive
		}
	}
	for i := range handlers.Placements {
		p := handlers.Placements[i]
		f := get(p.Service, p.Method)
		f.placement, f.exclusive = &p, f.exclusive || p.Exclusive
	}
	for _, lost := range handlers.Lost {
		get(lost.Service, lost.Method).lost[lost.NodeID] = true
	}
	for _, node := range handlers.Nodes {
		if !live(node.NodeID) {
			continue
		}
		for _, r := range node.Handlers {
			f := get(r.Service, r.Method)
			f.register[node.NodeID] = true
			f.exclusive = f.exclusive || r.Exclusive
		}
	}

	services := map[grove.ServiceID]*applicationServiceRow{}
	serviceRow := func(id grove.ServiceID) *applicationServiceRow {
		if services[id] == nil {
			services[id] = &applicationServiceRow{ServiceID: id, Name: applicationServiceName(id)}
		}
		return services[id]
	}
	keys := make([]handlerKey, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].service < keys[j].service || keys[i].service == keys[j].service && keys[i].method < keys[j].method
	})
	for _, key := range keys {
		f := byKey[key]
		service := serviceRow(key.service)
		row := applicationHandlerRow{Method: key.method, Name: fmt.Sprintf("method %d", key.method), Scaling: handlerScalingAutomatic}
		if f.spec != nil && f.spec.Name != "" {
			row.Name = f.spec.Name
		}
		if f.exclusive {
			row.Scaling = handlerScalingExclusive
		}
		if f.spec != nil {
			row.Capability = f.spec.Capability
		}
		var placed []string
		if f.placement != nil {
			row.Epoch, row.Capability = f.placement.Epoch, cmpString(f.placement.Capability, row.Capability)
			for _, node := range f.placement.Nodes {
				placed = append(placed, node.NodeID)
			}
		}
		sort.Strings(placed)
		seen := map[string]bool{}
		add := func(nodeID, state string) {
			seen[nodeID] = true
			row.Placements = append(row.Placements, applicationPlacementRow{
				NodeID: nodeID, Status: state, Execution: executions[nodeService{nodeID, key.service}],
				Debug: debugPlacement(service.Name, nodeID),
			})
		}
		for _, nodeID := range placed {
			add(nodeID, placementHealthy)
		}
		for _, nodeID := range sortedKeys(f.lost) {
			if !seen[nodeID] {
				add(nodeID, placementLost)
			}
		}
		// Nodes that merely registered an exclusive handler are standby
		// candidates; they are placements only while ownership is unassigned.
		if !f.exclusive || len(placed) != 1 {
			for _, nodeID := range sortedKeys(f.register) {
				if !seen[nodeID] {
					add(nodeID, placementStarting)
				}
			}
		}
		for i := range row.Placements {
			row.Placements[i].ID = fmt.Sprintf("%s/%d", strings.ToLower(service.Name), i+1)
		}
		if row.Scaling == handlerScalingExclusive {
			if len(placed) == 1 {
				row.Owner = placed[0]
				for i := range row.Placements {
					row.Placements[i].Owner = row.Placements[i].NodeID == row.Owner
				}
			} else {
				row.Transfer = true
			}
		}
		service.Handlers = append(service.Handlers, row)
	}

	// Services placed as a whole have no handler declarations; show their
	// single placement so the list stays complete.
	for _, placement := range status.Placements {
		if services[placement.ServiceID] != nil {
			continue
		}
		service := serviceRow(placement.ServiceID)
		state := placementHealthy
		if placement.Health != string(systemnats.ComponentHealthy) && placement.Health != string(systemnats.ComponentDebugging) {
			state = placementLost
		}
		service.Handlers = []applicationHandlerRow{{
			Name: "(whole service)", Scaling: handlerScalingService,
			Placements: []applicationPlacementRow{{
				ID: strings.ToLower(service.Name) + "/1", NodeID: placement.NodeID, Status: state,
				Execution: executions[nodeService{placement.NodeID, placement.ServiceID}],
				Debug:     debugPlacement(service.Name, placement.NodeID),
			}},
		}}
	}

	view := applicationServicesView{Services: []applicationServiceRow{}, Nodes: []applicationNodeRow{}}
	for _, service := range services {
		service.Status = serviceStatus(*service)
		view.Services = append(view.Services, *service)
	}
	sort.Slice(view.Services, func(i, j int) bool { return view.Services[i].ServiceID < view.Services[j].ServiceID })

	nodes := map[string]*applicationNodeRow{}
	nodeRow := func(id string) *applicationNodeRow {
		if nodes[id] == nil {
			nodes[id] = &applicationNodeRow{NodeID: id, Health: health[id], Hosted: []applicationHostedRow{}, Processes: []applicationProcessRow{}}
			if nodes[id].Health == "" {
				nodes[id].Health = "unknown"
			}
		}
		return nodes[id]
	}
	for _, node := range status.Nodes {
		row := nodeRow(node.NodeID)
		byProcess := map[string]int{}
		for _, component := range node.Components {
			if component.ProcessID == "" {
				continue
			}
			index, ok := byProcess[component.ProcessID]
			if !ok {
				index = len(row.Processes)
				byProcess[component.ProcessID] = index
				row.Processes = append(row.Processes, applicationProcessRow{
					Execution: executions[nodeService{node.NodeID, component.ServiceID}],
				})
			}
			row.Processes[index].Services = append(row.Processes[index].Services, component.Name)
		}
	}
	for _, service := range view.Services {
		for _, handler := range service.Handlers {
			for _, placement := range handler.Placements {
				node := nodeRow(placement.NodeID)
				node.Hosted = append(node.Hosted, applicationHostedRow{
					Service: service.Name, Handler: handler.Name, Scaling: handler.Scaling,
					Status: placement.Status, Owner: placement.Owner,
					Execution: placement.Execution,
				})
			}
		}
	}
	for _, node := range nodes {
		view.Nodes = append(view.Nodes, *node)
	}
	sort.Slice(view.Nodes, func(i, j int) bool { return view.Nodes[i].NodeID < view.Nodes[j].NodeID })
	return view
}

// debugPlacement attaches a debugger to service's placement on nodeID.
func debugPlacement(service, nodeID string) consoleview.Invocation {
	return consoleview.Invocation{Name: "debug.attach", Args: []string{service, "--node", nodeID}}
}

// serviceStatus summarizes handler placement health: recovering while any
// placement is lost or starting or an exclusive owner is being transferred,
// and unavailable only when no handler has a healthy placement at all.
func serviceStatus(service applicationServiceRow) string {
	anyHealthy, anyProblem := false, false
	for _, handler := range service.Handlers {
		for _, placement := range handler.Placements {
			if placement.Status == placementHealthy {
				anyHealthy = true
			} else {
				anyProblem = true
			}
		}
		anyProblem = anyProblem || handler.Transfer
	}
	switch {
	case !anyProblem:
		return placementHealthy
	case anyHealthy:
		return "recovering"
	}
	return "unavailable"
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cmpString(preferred, fallback string) string {
	if preferred != "" {
		return preferred
	}
	return fallback
}

// appServices reads the resolved handler placement view from a healthy node
// and renders it against current cluster status.
func (c *applicationController) appServices(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	target, attached := c.inspection()
	if !attached {
		status, _ := c.status(ctx)
		view := buildServicesView(systemnats.HandlerPlacementView{}, status)
		view.Error = "not deployed"
		return view, nil
	}
	status, statusErr := target.status(ctx)
	handlers, err := readHandlerPlacement(ctx, target, status)
	view := buildServicesView(handlers, status)
	if err != nil && applicationDeclaresHandlers() {
		view.Error = err.Error()
	} else if statusErr != nil {
		view.Error = statusErr.Error()
	}
	return view, nil
}

// readHandlerPlacement reads the cluster's resolved handler placement. Apps
// that declare no handlers legitimately have none.
func readHandlerPlacement(ctx context.Context, target inspectionTarget, status ClusterStatus) (systemnats.HandlerPlacementView, error) {
	if !applicationDeclaresHandlers() || target.url == "" {
		return systemnats.HandlerPlacementView{}, nil
	}
	inspector, err := target.inspector(ctx)
	if err != nil {
		return systemnats.HandlerPlacementView{}, err
	}
	return inspector.Handlers(ctx, status)
}
