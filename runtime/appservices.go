package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	placementHealthy  = "healthy"
	placementLost     = "lost"
	placementStarting = "starting"

	handlerScalingAutomatic = "automatic"
	handlerScalingExclusive = "exclusive"
	handlerScalingService   = "service"
)

// applicationPlacementRow is one concrete placement of a handler on a node.
type applicationPlacementRow struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Status string `json:"status"`
	Owner  bool   `json:"owner,omitempty"`
}

// applicationHandlerRow is one registered handler and where it runs.
type applicationHandlerRow struct {
	Method     grove.MethodID            `json:"method"`
	Name       string                    `json:"name"`
	Scaling    string                    `json:"scaling"`
	Capability string                    `json:"capability,omitempty"`
	Epoch      uint64                    `json:"epoch,omitempty"`
	Owner      string                    `json:"owner,omitempty"`
	Transfer   bool                      `json:"transferring,omitempty"`
	Placements []applicationPlacementRow `json:"placements"`
}

type applicationServiceRow struct {
	ServiceID grove.ServiceID         `json:"service_id"`
	Name      string                  `json:"name"`
	Status    string                  `json:"status"`
	Handlers  []applicationHandlerRow `json:"handlers"`
}

// applicationHostedRow is the inverse relation: a placement hosted by a node.
type applicationHostedRow struct {
	Service string `json:"service"`
	Handler string `json:"handler"`
	Scaling string `json:"scaling"`
	Status  string `json:"status"`
	Owner   bool   `json:"owner,omitempty"`
}

type applicationNodeRow struct {
	NodeID string                 `json:"node_id"`
	Health string                 `json:"health"`
	Hosted []applicationHostedRow `json:"hosted"`
}

type applicationServicesView struct {
	Services []applicationServiceRow `json:"services"`
	Nodes    []applicationNodeRow    `json:"nodes"`
	Error    string                  `json:"error,omitempty"`
}

func (s applicationServiceRow) placementCount() int {
	count := 0
	for _, handler := range s.Handlers {
		count += len(handler.Placements)
	}
	return count
}

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
			row.Placements = append(row.Placements, applicationPlacementRow{NodeID: nodeID, Status: state})
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
			nodes[id] = &applicationNodeRow{NodeID: id, Health: health[id], Hosted: []applicationHostedRow{}}
			if nodes[id].Health == "" {
				nodes[id].Health = "unknown"
			}
		}
		return nodes[id]
	}
	for _, node := range status.Nodes {
		nodeRow(node.NodeID)
	}
	for _, service := range view.Services {
		for _, handler := range service.Handlers {
			for _, placement := range handler.Placements {
				node := nodeRow(placement.NodeID)
				node.Hosted = append(node.Hosted, applicationHostedRow{
					Service: service.Name, Handler: handler.Name, Scaling: handler.Scaling,
					Status: placement.Status, Owner: placement.Owner,
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
	status, statusErr := c.status(ctx)
	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()
	if cluster == nil {
		view := buildServicesView(systemnats.HandlerPlacementView{}, status)
		view.Error = "not deployed"
		return view, nil
	}
	handlers, err := readHandlerPlacement(ctx, cluster.systemNATSURL, status)
	view := buildServicesView(handlers, status)
	if err != nil && applicationDeclaresHandlers() {
		view.Error = err.Error()
	} else if statusErr != nil {
		view.Error = statusErr.Error()
	}
	return view, nil
}

// readHandlerPlacement asks healthy nodes in turn for their resolved view.
// Apps that declare no handlers legitimately have none.
func readHandlerPlacement(ctx context.Context, systemNATSURL string, status ClusterStatus) (systemnats.HandlerPlacementView, error) {
	if !applicationDeclaresHandlers() || systemNATSURL == "" {
		return systemnats.HandlerPlacementView{}, nil
	}
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		return systemnats.HandlerPlacementView{}, err
	}
	defer transport.Close()
	var lastErr error = fmt.Errorf("no healthy node reported handler placement")
	for _, node := range status.Nodes {
		if node.Health != string(systemnats.HealthHealthy) {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		view, err := transport.RequestHandlerPlacement(requestCtx, node.NodeID)
		cancel()
		if err == nil && view.Ready {
			return view, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	return systemnats.HandlerPlacementView{}, lastErr
}
