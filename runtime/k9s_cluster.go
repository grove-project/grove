package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
)

const clusterHintLine = "[aqua::b]<enter>[-:-:-] Inspect node   [aqua::b]<l>[-:-:-] Logs   [aqua::b]<esc>[-:-:-] Back"

// showCluster opens the Cluster flow: an infrastructure/runtime perspective
// answering where and how the application is running, independent of the
// Services flow's service/handler drill-down. It is primarily observational
// and introduces no whole-cluster restart/repair actions.
func (v *applicationTUI) showCluster() {
	if !v.clusterOpen {
		clusterCtx, cancel := context.WithCancel(v.ctx)

		header := tview.NewTextView()
		header.SetDynamicColors(true)
		header.SetBorder(true)
		header.SetTitle(" [::b]Cluster[-:-:-] ")
		header.SetBorderColor(tcell.ColorDarkCyan)
		header.SetBackgroundColor(tcell.ColorDefault)

		table := tview.NewTable()
		table.SetBorder(true)
		table.SetBorderColor(tcell.ColorDarkCyan)
		table.SetBorderFocusColor(tcell.ColorAqua)
		table.SetFixed(1, 0)
		table.SetSelectable(true, false)
		table.SetSelectedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorAqua).Bold(true))
		table.SetInputCapture(v.clusterListKeyboard)

		node := tview.NewTextView()
		node.SetDynamicColors(true)
		node.SetScrollable(true)
		node.SetWrap(false)
		node.SetBorder(true)
		node.SetBorderColor(tcell.ColorDarkCyan)
		node.SetBorderFocusColor(tcell.ColorAqua)
		node.SetInputCapture(v.clusterNodeKeyboard)

		pages := tview.NewPages()
		pages.AddPage("list", table, true, true)
		pages.AddPage("node", node, true, false)

		hint := tview.NewTextView()
		hint.SetDynamicColors(true)
		hint.SetTextAlign(tview.AlignCenter)
		hint.SetBackgroundColor(tcell.ColorDefault)
		hint.SetText(clusterHintLine)

		layout := tview.NewFlex().SetDirection(tview.FlexRow)
		layout.AddItem(header, 5, 0, false)
		layout.AddItem(pages, 0, 1, true)
		layout.AddItem(hint, 1, 0, false)

		v.clusterHeader, v.clusterTable, v.clusterNode, v.clusterHint, v.clusterPages = header, table, node, hint, pages
		v.clusterOpen = true
		v.clusterCancel = cancel
		v.clusterNodeID = ""
		v.pages.AddPage(applicationTUIClusterPage, layout, true, true)
		v.app.SetFocus(table)
		go v.watchCluster(clusterCtx)
	}
	go v.refreshCluster(v.ctx)
}

func (v *applicationTUI) watchCluster(ctx context.Context) {
	ticker := time.NewTicker(applicationLogsRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.queueUpdate(func() {
				if v.clusterOpen {
					go v.refreshCluster(ctx)
				}
			})
		}
	}
}

func (v *applicationTUI) refreshCluster(ctx context.Context) {
	attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := v.tui.Select(attemptCtx, "cluster.nodes", nil)
	view, ok := result.(applicationClusterView)
	if err != nil || !ok {
		view = applicationClusterView{Error: "unable to read cluster state"}
		if err != nil {
			view.Error = err.Error()
		}
	}
	v.queueUpdate(func() {
		if !v.clusterOpen {
			return
		}
		v.clusterData = view
		v.renderCluster()
	})
}

func (v *applicationTUI) findClusterNode(nodeID string) (applicationClusterNodeRow, bool) {
	for _, node := range v.clusterData.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return applicationClusterNodeRow{}, false
}

// renderCluster redraws whichever level is currently displayed. A node
// selection whose node has disappeared from the latest view falls back to
// the node list rather than showing stale detail.
func (v *applicationTUI) renderCluster() {
	if !v.clusterOpen {
		return
	}
	v.renderClusterHeader()
	if v.clusterNodeID != "" {
		if node, ok := v.findClusterNode(v.clusterNodeID); ok {
			v.renderClusterNode(node)
			return
		}
		v.clusterNodeID = ""
		v.clusterPages.SwitchToPage("list")
		v.clusterHint.SetText(clusterHintLine)
		v.app.SetFocus(v.clusterTable)
	}
	v.renderClusterList()
}

func (v *applicationTUI) renderClusterHeader() {
	view := v.clusterData
	healthColor := "green"
	if view.Health == "degraded" || view.Health == "failed" {
		healthColor = "orangered"
	} else if view.Health == "not-deployed" || view.Health == "unknown" {
		healthColor = "gray"
	}
	first := fmt.Sprintf(
		"[::b]STATUS[-:-:-] [%s::b]%s[-:-:-]   [::b]NODES[-:-:-] %d/%d   [::b]SERVICES[-:-:-] %d/%d",
		healthColor,
		strings.ToUpper(displayTUIValue(view.Health)),
		view.NodesHealthy,
		view.NodesTotal,
		view.ServicesHealthy,
		view.ServicesTotal,
	)
	second := fmt.Sprintf(
		"[::b]VERSION[-:-:-] %s   [::b]CONFIG[-:-:-] %s   [::b]UPTIME[-:-:-] %s",
		displayTUIValue(view.Version),
		displayTUIValue(view.ConfigRevision),
		displayTUIValue(view.Uptime),
	)
	text := first + "\n" + second
	if view.Error != "" {
		text += "\n[orangered::b]" + tview.Escape(view.Error) + "[-:-:-]"
	}
	v.clusterHeader.SetText(text)
}

func (v *applicationTUI) renderClusterList() {
	table := v.clusterTable
	selectedRow, _ := table.GetSelection()
	table.Clear()
	table.SetTitle(" [::b]CLUSTER / NODES[-:-:-] ")
	for column, name := range []string{"NODE", "STATUS", "SERVICES", "LAST HEARTBEAT"} {
		table.SetCell(0, column, tview.NewTableCell(name).
			SetTextColor(tcell.ColorAqua).SetAttributes(tcell.AttrBold).SetSelectable(false).SetExpansion(1))
	}
	now := time.Now()
	for index, node := range v.clusterData.Nodes {
		row := index + 1
		cells := []string{node.NodeID, node.Health, fmt.Sprint(len(node.Hosted)), formatLastHeartbeat(node.LastSeen, now)}
		color := statusColor(node.Health)
		for column, text := range cells {
			cell := tview.NewTableCell(text).SetExpansion(1)
			if color != tcell.ColorDefault && column == 1 {
				cell.SetTextColor(color)
			}
			table.SetCell(row, column, cell)
		}
	}
	if len(v.clusterData.Nodes) == 0 {
		cell := tview.NewTableCell("No nodes reported")
		cell.SetTextColor(tcell.ColorGray)
		cell.SetSelectable(false)
		table.SetCell(1, 0, cell)
		return
	}
	if selectedRow < 1 {
		selectedRow = 1
	}
	if selectedRow > len(v.clusterData.Nodes) {
		selectedRow = len(v.clusterData.Nodes)
	}
	table.Select(selectedRow, 0)
}

func (v *applicationTUI) selectedClusterNodeID() (string, bool) {
	row, _ := v.clusterTable.GetSelection()
	if row < 1 || row > len(v.clusterData.Nodes) {
		return "", false
	}
	return v.clusterData.Nodes[row-1].NodeID, true
}

func (v *applicationTUI) openClusterNode(nodeID string) {
	v.clusterNodeID = nodeID
	v.clusterPages.SwitchToPage("node")
	v.app.SetFocus(v.clusterNode)
	if node, ok := v.findClusterNode(nodeID); ok {
		v.renderClusterNode(node)
	}
}

func (v *applicationTUI) closeClusterNode() {
	v.clusterNodeID = ""
	v.clusterPages.SwitchToPage("list")
	v.clusterHint.SetText(clusterHintLine)
	v.app.SetFocus(v.clusterTable)
}

func (v *applicationTUI) clusterListKeyboard(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		v.closeCluster()
		return nil
	case tcell.KeyEnter, tcell.KeyRight:
		if nodeID, ok := v.selectedClusterNodeID(); ok {
			v.openClusterNode(nodeID)
		}
		return nil
	case tcell.KeyRune:
	default:
		return event
	}
	switch event.Rune() {
	case 'q':
		v.closeCluster()
	case 'j':
		return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	case 'k':
		return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
	case 'l':
		if nodeID, ok := v.selectedClusterNodeID(); ok {
			v.closeCluster()
			v.showLogs(nodeID)
		}
	default:
		return event
	}
	return nil
}

func (v *applicationTUI) clusterNodeKeyboard(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape, tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		v.closeClusterNode()
		return nil
	}
	if event.Key() != tcell.KeyRune {
		return event
	}
	switch event.Rune() {
	case 'q':
		v.closeCluster()
	case 'l':
		nodeID := v.clusterNodeID
		v.closeCluster()
		v.showLogs(nodeID)
	default:
		return event
	}
	return nil
}

func (v *applicationTUI) closeCluster() {
	if v.clusterCancel != nil {
		v.clusterCancel()
	}
	v.pages.RemovePage(applicationTUIClusterPage)
	v.clusterHeader, v.clusterTable, v.clusterNode, v.clusterHint, v.clusterPages = nil, nil, nil, nil, nil
	v.clusterOpen = false
	v.clusterCancel = nil
	v.clusterNodeID = ""
	v.app.SetFocus(v.table)
}

// renderClusterNode draws the node detail screen: runtime state and hosted
// service placements, discovered generically from the same control-plane
// read model the Services flow uses.
func (v *applicationTUI) renderClusterNode(node applicationClusterNodeRow) {
	v.clusterNode.SetTitle(" [::b]CLUSTER / NODES / " + tview.Escape(strings.ToUpper(node.NodeID)) + "[-:-:-] ")
	row, column := v.clusterNode.GetScrollOffset()
	v.clusterNode.SetText(renderClusterNodeDetail(node, v.clusterData, time.Now()))
	v.clusterNode.ScrollTo(row, column)
	if v.clusterHint != nil {
		v.clusterHint.SetText("[aqua::b]<l>[-:-:-] Logs   [aqua::b]<esc>[-:-:-] Back")
	}
}

func renderClusterNodeDetail(node applicationClusterNodeRow, cluster applicationClusterView, now time.Time) string {
	var out strings.Builder
	field := func(label, value string) {
		fmt.Fprintf(&out, "  %-14s%s\n", label, tview.Escape(displayTUIValue(value)))
	}
	section := func(title string) { fmt.Fprintf(&out, "\n[aqua::b]%s[-:-:-]\n", title) }

	field("STATUS", strings.ToUpper(node.Health))
	field("VERSION", cluster.Version)
	field("CONFIG", cluster.ConfigRevision)

	section("SERVICES")
	if len(node.Hosted) == 0 {
		out.WriteString("  [gray::]No services hosted on this node.[-:-:-]\n")
	} else {
		fmt.Fprintf(&out, "  [aqua::b]%-20s %-20s %-14s %s[-:-:-]\n", "SERVICE", "HANDLER", "STATUS", "ROLE")
		for _, hosted := range node.Hosted {
			role := "-"
			if hosted.Owner {
				role = "owner"
			}
			fmt.Fprintf(&out, "  %-20s %-20s %-14s %s\n",
				tview.Escape(hosted.Service), tview.Escape(hosted.Handler), tview.Escape(hosted.Status), role)
		}
	}

	section("RUNTIME")
	field("Grovelet", strings.ToUpper(node.Health))
	systemNATS := "reachable"
	if node.Error != "" {
		systemNATS = "unreachable: " + node.Error
	}
	field("System NATS", systemNATS)
	field("Last heartbeat", formatLastHeartbeat(node.LastSeen, now))

	return out.String()
}

// formatLastHeartbeat renders an RFC3339Nano heartbeat timestamp as an
// elapsed duration, matching the generic liveness signal System NATS already
// derives for every node.
func formatLastHeartbeat(lastSeen string, now time.Time) string {
	if lastSeen == "" {
		return "-"
	}
	seen, err := time.Parse(time.RFC3339Nano, lastSeen)
	if err != nil {
		return "-"
	}
	elapsed := now.Sub(seen)
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed.Round(time.Millisecond).String() + " ago"
}
