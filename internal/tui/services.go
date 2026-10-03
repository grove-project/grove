package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/internal/consoleview"
)

const applicationTUIServicesPage = "services"

// servicesLocation is where the drill-down currently is, kept by identity so
// live updates never move the operator to a different service or node.
type servicesLocation struct {
	nodes      bool // node -> hosted placements instead of service -> handler
	service    grove.ServiceID
	hasService bool
	method     grove.MethodID
	hasHandler bool
	node       string
	hasNode    bool
}

type servicesRow struct {
	cells   []string
	service grove.ServiceID
	method  grove.MethodID
	node    string
	color   tcell.Color
	// debug attaches a debugger to the row's placement, when one can.
	debug consoleview.Invocation
}

// servicesScreen is one rendered level of the drill-down.
type servicesScreen struct {
	title   string
	columns []string
	rows    []servicesRow
	hint    string
	level   string // services, handlers, placements, nodes, hosted
	service string
	handler string
}

const servicesHintBase = "[aqua::b]<enter>[-:-:-] Drill in  [aqua::b]<esc>[-:-:-] Back  [aqua::b]<n>[-:-:-] Nodes  [aqua::b]<q>[-:-:-] Close"

func statusColor(status string) tcell.Color {
	switch status {
	case consoleview.PlacementHealthy:
		return tcell.ColorGreen
	case consoleview.PlacementLost, "unavailable":
		return tcell.ColorOrangeRed
	case consoleview.PlacementStarting, "recovering":
		return tcell.ColorYellow
	}
	return tcell.ColorDefault
}

func findService(view consoleview.Services, id grove.ServiceID) (consoleview.Service, bool) {
	for _, service := range view.Services {
		if service.ServiceID == id {
			return service, true
		}
	}
	return consoleview.Service{}, false
}

func findHandler(service consoleview.Service, method grove.MethodID) (consoleview.Handler, bool) {
	for _, handler := range service.Handlers {
		if handler.Method == method {
			return handler, true
		}
	}
	return consoleview.Handler{}, false
}

// buildServicesScreen renders the level named by location. A location whose
// service, handler, or node has disappeared falls back to the nearest level
// that still exists.
func buildServicesScreen(view consoleview.Services, location servicesLocation) servicesScreen {
	if location.nodes {
		if location.hasNode {
			for _, node := range view.Nodes {
				if node.NodeID == location.node {
					return hostedScreen(node)
				}
			}
		}
		return nodesScreen(view)
	}
	if location.hasService {
		if service, ok := findService(view, location.service); ok {
			if location.hasHandler {
				if handler, ok := findHandler(service, location.method); ok {
					return placementsScreen(service, handler)
				}
			}
			return handlersScreen(service)
		}
	}
	return servicesListScreen(view)
}

func servicesListScreen(view consoleview.Services) servicesScreen {
	screen := servicesScreen{
		title: "APP / SERVICES", level: "services",
		columns: []string{"SERVICE", "HANDLERS", "PLACEMENTS", "STATUS"},
		hint:    servicesHintBase,
	}
	for _, service := range view.Services {
		screen.rows = append(screen.rows, servicesRow{
			cells:   []string{service.Name, fmt.Sprint(len(service.Handlers)), fmt.Sprint(service.PlacementCount()), service.Status},
			service: service.ServiceID, color: statusColor(service.Status),
		})
	}
	return screen
}

func handlersScreen(service consoleview.Service) servicesScreen {
	screen := servicesScreen{
		title: "APP / SERVICES / " + strings.ToUpper(service.Name), level: "handlers", service: service.Name,
		columns: []string{"HANDLER", "SCALING", "PLACEMENTS", "OWNER"},
		hint:    servicesHintBase,
	}
	for _, handler := range service.Handlers {
		owner := "-"
		switch {
		case handler.Transfer:
			owner = "transferring"
		case handler.Owner != "":
			owner = handler.Owner
		}
		color := tcell.ColorDefault
		if handler.Transfer {
			color = tcell.ColorYellow
		}
		screen.rows = append(screen.rows, servicesRow{
			cells:   []string{handler.Name, handler.Scaling, fmt.Sprint(len(handler.Placements)), owner},
			service: service.ServiceID, method: handler.Method, color: color,
		})
	}
	return screen
}

func placementsScreen(service consoleview.Service, handler consoleview.Handler) servicesScreen {
	screen := servicesScreen{
		title: "APP / SERVICES / " + strings.ToUpper(service.Name) + " / " + strings.ToUpper(handler.Name), level: "placements",
		service: service.Name, handler: handler.Name,
		columns: []string{"PLACEMENT", "NODE", "STATUS", "ROLE", "PROCESS"},
		hint:    servicesHintBase + "  [aqua::b]<d>[-:-:-] Debug  [aqua::b]<l>[-:-:-] Logs",
	}
	if handler.Transfer {
		screen.hint = "[yellow::b]Exclusive ownership of " + tview.Escape(handler.Capability) + " is being transferred[-:-:-]\n" + screen.hint
	}
	for _, placement := range handler.Placements {
		role := "-"
		if placement.Owner {
			role = fmt.Sprintf("owner (epoch %d)", handler.Epoch)
		}
		screen.rows = append(screen.rows, servicesRow{
			cells:   []string{placement.ID, placement.NodeID, placement.Status, role, placement.Label()},
			service: service.ServiceID, method: handler.Method, node: placement.NodeID, color: statusColor(placement.Status),
			debug: placement.Debug,
		})
	}
	return screen
}

func nodesScreen(view consoleview.Services) servicesScreen {
	screen := servicesScreen{
		title: "CLUSTER / NODES", level: "nodes",
		columns: []string{"NODE", "HEALTH", "PLACEMENTS", "PROCESSES"},
		hint:    servicesHintBase,
	}
	for _, node := range view.Nodes {
		screen.rows = append(screen.rows, servicesRow{
			cells: []string{node.NodeID, node.Health, fmt.Sprint(len(node.Hosted)), fmt.Sprint(len(node.Processes))},
			node:  node.NodeID, color: statusColor(node.Health),
		})
	}
	return screen
}

func hostedScreen(node consoleview.Node) servicesScreen {
	screen := servicesScreen{
		title: "CLUSTER / NODES / " + strings.ToUpper(node.NodeID), level: "hosted",
		columns: []string{"SERVICE", "HANDLER", "SCALING", "STATUS", "ROLE", "PROCESS"},
		hint:    servicesHintBase,
	}
	for _, hosted := range node.Hosted {
		role := "-"
		if hosted.Owner {
			role = "owner"
		}
		screen.rows = append(screen.rows, servicesRow{
			cells: []string{hosted.Service, hosted.Handler, hosted.Scaling, hosted.Status, role, hosted.Label()},
			node:  node.NodeID, color: statusColor(hosted.Status),
		})
	}
	return screen
}

// drillDown moves the location one level deeper into row.
func (l servicesLocation) drillDown(screen servicesScreen, row servicesRow) servicesLocation {
	switch screen.level {
	case "services":
		l.service, l.hasService = row.service, true
	case "handlers":
		l.method, l.hasHandler = row.method, true
	case "nodes":
		l.node, l.hasNode = row.node, true
	}
	return l
}

// up moves one level toward the top and reports whether it was already there.
func (l servicesLocation) up() (servicesLocation, bool) {
	switch {
	case l.nodes && l.hasNode:
		l.hasNode = false
	case !l.nodes && l.hasHandler:
		l.hasHandler = false
	case !l.nodes && l.hasService:
		l.hasService = false
	default:
		return l, true
	}
	return l, false
}

func (v *applicationTUI) showServices(action string, nodes bool) {
	v.servicesAction = action
	v.servicesLocation = servicesLocation{nodes: nodes}
	if !v.servicesOpen {
		servicesCtx, cancel := context.WithCancel(v.ctx)
		table := tview.NewTable()
		table.SetBorder(true)
		table.SetBorderColor(tcell.ColorDarkCyan)
		table.SetBorderFocusColor(tcell.ColorAqua)
		table.SetFixed(1, 0)
		table.SetSelectable(true, false)
		table.SetSelectedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorAqua).Bold(true))
		table.SetInputCapture(v.servicesKeyboard)
		hint := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter)
		v.servicesTable, v.servicesHint = table, hint
		layout := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(table, 0, 1, true).AddItem(hint, 2, 0, false)
		v.servicesOpen = true
		v.servicesCancel = cancel
		v.pages.AddPage(applicationTUIServicesPage, layout, true, true)
		v.app.SetFocus(table)
		go v.watchServices(servicesCtx)
	}
	v.renderServices()
	go v.refreshServices(v.ctx)
}

func (v *applicationTUI) renderServices() {
	if !v.servicesOpen {
		return
	}
	screen := buildServicesScreen(v.servicesData, v.servicesLocation)
	selectedRow, _ := v.servicesTable.GetSelection()
	v.servicesTable.Clear()
	v.servicesTable.SetTitle(" [::b]" + tview.Escape(screen.title) + "[-:-:-] ")
	for column, name := range screen.columns {
		v.servicesTable.SetCell(0, column, tview.NewTableCell(name).
			SetTextColor(tcell.ColorAqua).SetAttributes(tcell.AttrBold).SetSelectable(false).SetExpansion(1))
	}
	for index, row := range screen.rows {
		for column, text := range row.cells {
			cell := tview.NewTableCell(text).SetExpansion(1)
			// The status column carries the color; other cells stay neutral.
			if row.color != tcell.ColorDefault && (screen.level != "handlers" || column == 3) && column >= len(row.cells)-2 {
				cell.SetTextColor(row.color)
			}
			v.servicesTable.SetCell(index+1, column, cell)
		}
	}
	if selectedRow < 1 {
		selectedRow = 1
	}
	if selectedRow > len(screen.rows) {
		selectedRow = len(screen.rows)
	}
	if len(screen.rows) != 0 {
		v.servicesTable.Select(selectedRow, 0)
	}
	hint := screen.hint
	if v.servicesData.Error != "" {
		hint = "[orangered::b]" + tview.Escape(v.servicesData.Error) + "[-:-:-]\n" + hint
	}
	v.servicesHint.SetText(hint)
}

func (v *applicationTUI) selectedServicesRow() (servicesScreen, servicesRow, bool) {
	screen := buildServicesScreen(v.servicesData, v.servicesLocation)
	row, _ := v.servicesTable.GetSelection()
	if row < 1 || row > len(screen.rows) {
		return screen, servicesRow{}, false
	}
	return screen, screen.rows[row-1], true
}

func (v *applicationTUI) servicesKeyboard(event *tcell.EventKey) *tcell.EventKey {
	up := func() {
		next, atTop := v.servicesLocation.up()
		if atTop {
			v.closeServices()
			return
		}
		v.servicesLocation = next
		v.servicesTable.Select(1, 0)
		v.renderServices()
	}
	switch event.Key() {
	case tcell.KeyEscape, tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		up()
		return nil
	case tcell.KeyEnter, tcell.KeyRight:
		if screen, row, ok := v.selectedServicesRow(); ok {
			v.servicesLocation = v.servicesLocation.drillDown(screen, row)
			v.servicesTable.Select(1, 0)
			v.renderServices()
		}
		return nil
	case tcell.KeyRune:
	default:
		return event
	}
	switch event.Rune() {
	case 'q':
		v.closeServices()
	case 'j':
		return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	case 'k':
		return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
	case 'n':
		v.servicesLocation = servicesLocation{nodes: !v.servicesLocation.nodes}
		v.servicesTable.Select(1, 0)
		v.renderServices()
	case 'd':
		screen, row, ok := v.selectedServicesRow()
		if !ok || screen.level != "placements" || row.debug.Name == "" {
			v.setFlash(tcell.ColorOrange, "Select a placement to debug")
			return nil
		}
		v.closeServices()
		v.activateActionName(row.debug.Name, row.debug.Args)
	case 'l':
		v.closeServices()
		if logs, ok := v.actionForView(console.ViewLogs); ok {
			v.activateActionName(logs.Name, nil)
		}
	default:
		return event
	}
	return nil
}

func (v *applicationTUI) watchServices(ctx context.Context) {
	ticker := time.NewTicker(applicationLogsRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.queueUpdate(func() {
				if v.servicesOpen {
					go v.refreshServices(ctx)
				}
			})
		}
	}
}

func (v *applicationTUI) refreshServices(ctx context.Context) {
	attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := v.tui.Select(attemptCtx, v.servicesAction, nil)
	view, ok := result.(consoleview.Services)
	if err != nil || !ok {
		view = consoleview.Services{Error: "unable to read services"}
		if err != nil {
			view.Error = err.Error()
		}
	}
	v.queueUpdate(func() {
		if !v.servicesOpen {
			return
		}
		v.servicesData = view
		v.renderServices()
	})
}

func (v *applicationTUI) closeServices() {
	if v.servicesCancel != nil {
		v.servicesCancel()
	}
	v.pages.RemovePage(applicationTUIServicesPage)
	v.servicesTable, v.servicesHint = nil, nil
	v.servicesOpen = false
	v.servicesCancel = nil
	v.app.SetFocus(v.table)
}

// actionForView returns the first offered action presented as view.
func (v *applicationTUI) actionForView(view console.View) (console.Action, bool) {
	for _, action := range v.actions {
		if action.View == view {
			return action, true
		}
	}
	return console.Action{}, false
}
