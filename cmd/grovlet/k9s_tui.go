package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/grove-project/grove/console"
)

const (
	applicationTUIRefreshInterval  = 250 * time.Millisecond
	applicationLogsRefreshInterval = 500 * time.Millisecond
	applicationTUIDialogPage       = "dialog"
	applicationTUILogsPage         = "logs"
	applicationTUIIngressRegion    = "ingress"
)

type applicationTUI struct {
	ctx    context.Context
	cancel context.CancelFunc
	tui    *console.TUI

	app    *tview.Application
	pages  *tview.Pages
	root   *tview.Flex
	header *tview.TextView
	table  *tview.Table
	flash  *tview.TextView
	prompt *tview.InputField
	hints  *tview.TextView

	actions        []console.Action
	visibleActions []console.Action
	filter         string
	promptMode     string
	dialogOpen     bool
	busy           bool
	busyAction     string
	busyStarted    time.Time
	spinnerFrame   int
	queueDraw      func(func())
	ingressURL     string
	openURL        func(string) error
	logsView       *tview.TextView
	logsOpen       bool
	logsCancel     context.CancelFunc
}

func runInteractiveApplicationTUI(ctx context.Context, tui *console.TUI) error {
	view, err := newApplicationTUI(ctx, tui)
	if err != nil {
		return err
	}
	defer view.cancel()
	return view.run()
}

func newApplicationTUI(parent context.Context, tui *console.TUI) (*applicationTUI, error) {
	ctx, cancel := context.WithCancel(parent)
	view := &applicationTUI{
		ctx:     ctx,
		cancel:  cancel,
		tui:     tui,
		app:     tview.NewApplication(),
		pages:   tview.NewPages(),
		header:  tview.NewTextView(),
		table:   tview.NewTable(),
		flash:   tview.NewTextView(),
		prompt:  tview.NewInputField(),
		hints:   tview.NewTextView(),
		actions: tui.Actions(),
		openURL: openBrowser,
	}
	view.queueDraw = func(update func()) { view.app.QueueUpdateDraw(update) }
	if len(view.actions) == 0 {
		cancel()
		return nil, fmt.Errorf("Grove Shop TUI has no actions")
	}
	view.configure()
	model, err := tui.ReadModel(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("read initial Grove Shop TUI model: %w", err)
	}
	view.updateModel(model)
	view.rebuildActions("")
	return view, nil
}

func (v *applicationTUI) configure() {
	v.header.SetDynamicColors(true)
	v.header.SetRegions(true)
	v.header.SetBorder(true)
	v.header.SetTitle(" [::b]Grove Shop[-:-:-] ")
	v.header.SetBorderColor(tcell.ColorDarkCyan)
	v.header.SetBackgroundColor(tcell.ColorDefault)
	v.header.SetMouseCapture(v.captureHeaderMouse)

	v.table.SetBorder(true)
	v.table.SetTitle(" [::b]Actions[-:-:-] ")
	v.table.SetBorderColor(tcell.ColorDarkCyan)
	v.table.SetBorderFocusColor(tcell.ColorAqua)
	v.table.SetBackgroundColor(tcell.ColorDefault)
	v.table.SetFixed(1, 0)
	v.table.SetSelectable(true, false)
	v.table.SetSelectedStyle(
		tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorAqua).Bold(true),
	)
	v.table.SetSelectionChangedFunc(func(row, _ int) {
		if v.busy || row < 1 || row > len(v.visibleActions) {
			return
		}
		action := v.visibleActions[row-1]
		v.setFlash(tcell.ColorGray, action.Name+" — "+action.Description)
	})

	v.flash.SetDynamicColors(true)
	v.flash.SetTextAlign(tview.AlignCenter)
	v.flash.SetBackgroundColor(tcell.ColorDefault)

	v.prompt.SetFieldBackgroundColor(tcell.ColorDefault)
	v.prompt.SetFieldTextColor(tcell.ColorWhite)
	v.prompt.SetLabelColor(tcell.ColorAqua)
	v.prompt.SetDoneFunc(v.promptDone)
	v.prompt.SetChangedFunc(v.promptChanged)

	v.hints.SetDynamicColors(true)
	v.hints.SetTextAlign(tview.AlignCenter)
	v.hints.SetBackgroundColor(tcell.ColorDefault)
	v.hints.SetText(k9sHintLine())

	v.root = tview.NewFlex().SetDirection(tview.FlexRow)
	v.root.SetBackgroundColor(tcell.ColorDefault)
	v.root.AddItem(v.header, 5, 0, false)
	v.root.AddItem(v.table, 0, 1, true)
	v.root.AddItem(v.flash, 1, 0, false)
	v.root.AddItem(v.prompt, 0, 0, false)
	v.root.AddItem(v.hints, 2, 0, false)

	v.pages.AddPage("main", v.root, true, true)
	v.app.SetRoot(v.pages, true)
	v.app.EnableMouse(true)
	v.app.SetFocus(v.table)
	v.app.SetInputCapture(v.keyboard)
}

func (v *applicationTUI) run() error {
	go v.watchModel()
	go func() {
		<-v.ctx.Done()
		v.app.Stop()
	}()
	if err := v.app.Run(); err != nil {
		return fmt.Errorf("run Grove Shop TUI: %w", err)
	}
	return nil
}

func (v *applicationTUI) watchModel() {
	ticker := time.NewTicker(applicationTUIRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-v.ctx.Done():
			return
		case <-ticker.C:
			model, err := v.tui.ReadModel(v.ctx)
			if err != nil {
				v.queueUpdate(func() {
					v.setFlash(tcell.ColorOrangeRed, "Status unavailable: "+err.Error())
				})
				continue
			}
			v.queueUpdate(func() {
				v.refreshModel(model)
			})
		}
	}
}

func (v *applicationTUI) queueUpdate(update func()) {
	select {
	case <-v.ctx.Done():
		return
	default:
		v.queueDraw(update)
	}
}

func (v *applicationTUI) refreshModel(model console.Model) {
	v.updateModel(model)
	if !v.busy {
		return
	}
	v.spinnerFrame++
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	elapsed := time.Since(v.busyStarted).Round(time.Second)
	v.setFlash(
		tcell.ColorAqua,
		fmt.Sprintf("%s %s in progress · %s", frames[v.spinnerFrame%len(frames)], v.busyAction, elapsed),
	)
}

func (v *applicationTUI) updateModel(model console.Model) {
	healthColor := "green"
	if model.Health == "degraded" || model.Health == "failed" {
		healthColor = "orangered"
	} else if model.Health == "not-deployed" {
		healthColor = "gray"
	}
	first := fmt.Sprintf(
		"[::b]CLUSTER[-:-:-] [%s::b]%s[-:-:-]   [::b]NODES[-:-:-] %d/%d   [::b]SERVICES[-:-:-] %d/%d",
		healthColor,
		strings.ToUpper(displayTUIValue(model.Health)),
		model.NodesHealthy,
		model.NodesTotal,
		model.ServicesHealthy,
		model.ServicesTotal,
	)
	second := fmt.Sprintf(
		"[::b]VERSION[-:-:-] %s   [::b]CONFIG[-:-:-] %s",
		displayTUIValue(model.ActiveVersion),
		displayTUIValue(model.ConfigRevision),
	)
	if model.CandidateRevision != "" {
		second += "   [::b]CANDIDATE[-:-:-] " + model.CandidateRevision
	}
	if model.RolloutPhase != "" {
		second += "   [::b]ROLLOUT[-:-:-] " + model.RolloutPhase
	}
	v.ingressURL = model.IngressURL
	third := "[::b]INGRESS[-:-:-] -"
	if model.IngressURL != "" {
		third = fmt.Sprintf(
			"[::b]INGRESS[-:-:-] [\"%s\"][aqua::u]%s[-:-:-][\"\"]  [gray](click to open)[-:-:-]",
			applicationTUIIngressRegion,
			model.IngressURL,
		)
	}
	lines := []string{first, second, third}
	for _, session := range model.DebugSessions {
		lines = append(lines, fmt.Sprintf(
			"[purple::b]DLV[-:-:-] %s  [gray]%s/%s[-:-:-]  [aqua::b]DAP %s[-:-:-]",
			tview.Escape(displayTUIValue(session.ServiceName)),
			tview.Escape(displayTUIValue(session.NodeID)),
			tview.Escape(displayTUIValue(session.WorkerID)),
			tview.Escape(displayTUIValue(session.DAPEndpoint)),
		))
	}
	v.root.ResizeItem(v.header, 5+len(model.DebugSessions), 0)
	v.header.SetText(strings.Join(lines, "\n"))
	if model.LastEvent != "" && !v.busy {
		v.setFlash(tcell.ColorLightGray, model.LastEvent)
	}
}

func (v *applicationTUI) captureHeaderMouse(
	action tview.MouseAction,
	event *tcell.EventMouse,
) (tview.MouseAction, *tcell.EventMouse) {
	if action != tview.MouseLeftClick || v.ingressURL == "" {
		return action, event
	}
	x, y := event.Position()
	innerX, innerY, _, _ := v.header.GetInnerRect()
	urlStart := innerX + len("INGRESS ")
	if y != innerY+2 || x < urlStart || x >= urlStart+len(v.ingressURL) {
		return action, event
	}
	if err := v.openURL(v.ingressURL); err != nil {
		v.setFlash(tcell.ColorOrangeRed, "Open ingress: "+err.Error())
	} else {
		v.setFlash(tcell.ColorGreen, "Opened "+v.ingressURL)
	}
	return action, nil
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("launch system browser: %w", err)
	}
	go func() { _ = command.Wait() }()
	return nil
}

func (v *applicationTUI) rebuildActions(filter string) {
	selectedName := v.selectedActionName()
	v.filter = filter
	v.visibleActions = v.visibleActions[:0]
	needle := strings.ToLower(strings.TrimSpace(filter))
	for _, action := range v.actions {
		haystack := strings.ToLower(strings.Join([]string{
			action.Section, action.Label, action.Name, action.Description,
		}, " "))
		if needle == "" || strings.Contains(haystack, needle) {
			v.visibleActions = append(v.visibleActions, action)
		}
	}
	v.table.Clear()
	headers := []string{"SECTION", "ACTION", "KEY", "DESCRIPTION"}
	for column, header := range headers {
		cell := tview.NewTableCell(header)
		cell.SetTextColor(tcell.ColorAqua)
		cell.SetAttributes(tcell.AttrBold)
		cell.SetSelectable(false)
		v.table.SetCell(0, column, cell)
	}
	selectedRow := 1
	for index, action := range v.visibleActions {
		row := index + 1
		values := []string{action.Section, action.Label, hotkeyLabel(action.Name), action.Description}
		for column, value := range values {
			cell := tview.NewTableCell(value)
			cell.SetTextColor(tcell.ColorWhite)
			if column == 3 {
				cell.SetExpansion(1)
			}
			v.table.SetCell(row, column, cell)
		}
		if action.Name == selectedName {
			selectedRow = row
		}
	}
	if len(v.visibleActions) == 0 {
		cell := tview.NewTableCell("No matching actions")
		cell.SetTextColor(tcell.ColorGray)
		cell.SetSelectable(false)
		v.table.SetCell(1, 0, cell)
		return
	}
	v.table.Select(selectedRow, 0)
}

func (v *applicationTUI) selectedActionName() string {
	row, _ := v.table.GetSelection()
	if row < 1 || row > len(v.visibleActions) {
		return ""
	}
	return v.visibleActions[row-1].Name
}

func (v *applicationTUI) selectedAction() (console.Action, bool) {
	row, _ := v.table.GetSelection()
	if row < 1 || row > len(v.visibleActions) {
		return console.Action{}, false
	}
	return v.visibleActions[row-1], true
}

func (v *applicationTUI) keyboard(event *tcell.EventKey) *tcell.EventKey {
	if v.logsOpen || v.dialogOpen || v.app.GetFocus() == v.prompt {
		return event
	}
	if event.Key() == tcell.KeyCtrlC {
		v.shutdown()
		return nil
	}
	if event.Key() == tcell.KeyEscape {
		if v.filter != "" {
			v.rebuildActions("")
			v.setFlash(tcell.ColorGray, "Filter cleared")
		}
		return nil
	}
	if event.Key() == tcell.KeyUp {
		v.moveSelection(-1)
		return nil
	}
	if event.Key() == tcell.KeyDown {
		v.moveSelection(1)
		return nil
	}
	if event.Key() == tcell.KeyEnter || event.Key() == tcell.KeyRight {
		v.runSelectedAction()
		return nil
	}
	if event.Key() != tcell.KeyRune {
		return event
	}
	switch event.Rune() {
	case 'q':
		v.shutdown()
	case 'j':
		v.moveSelection(1)
	case 'k':
		v.moveSelection(-1)
	case 'g':
		v.selectFirst()
	case 'G':
		v.selectLast()
	case ':':
		v.openPrompt("command")
	case '/':
		v.openPrompt("filter")
	case '?':
		v.showHelp()
	default:
		if actionName, ok := actionForHotkey(event.Rune()); ok {
			v.activateActionName(actionName, nil)
			return nil
		}
		return event
	}
	return nil
}

func (v *applicationTUI) moveSelection(delta int) {
	if len(v.visibleActions) == 0 {
		return
	}
	row, _ := v.table.GetSelection()
	row += delta
	if row < 1 {
		row = len(v.visibleActions)
	} else if row > len(v.visibleActions) {
		row = 1
	}
	v.table.Select(row, 0)
}

func (v *applicationTUI) selectFirst() {
	if len(v.visibleActions) != 0 {
		v.table.Select(1, 0)
	}
}

func (v *applicationTUI) selectLast() {
	if len(v.visibleActions) != 0 {
		v.table.Select(len(v.visibleActions), 0)
	}
}

func (v *applicationTUI) runSelectedAction() {
	action, ok := v.selectedAction()
	if !ok {
		return
	}
	v.activateAction(action, nil)
}

func (v *applicationTUI) activateActionName(name string, args []string) {
	for _, action := range v.actions {
		if action.Name == name {
			v.activateAction(action, args)
			return
		}
	}
	v.setFlash(tcell.ColorOrangeRed, "Unknown action: "+name)
}

func (v *applicationTUI) activateAction(action console.Action, args []string) {
	if v.busy {
		v.setFlash(tcell.ColorOrange, "An operation is already running")
		return
	}
	if action.Name == "logs.view" {
		v.showLogs()
		return
	}
	if len(args) == 0 {
		switch action.Name {
		case "rollout.start":
			v.showArgumentDialog(action, "New rollout", "Config path", "configs/acme.yaml", func(value string) []string {
				return []string{"--config", strings.TrimSpace(value)}
			})
			return
		case "debug.attach":
			v.showArgumentDialog(action, "Attach debugger", "Arguments", "orders --listen 127.0.0.1:40000", strings.Fields)
			return
		}
	}
	v.startAction(action, args)
}

func (v *applicationTUI) startAction(action console.Action, args []string) {
	v.busy = true
	v.busyAction = action.Label
	v.busyStarted = time.Now()
	v.spinnerFrame = 0
	v.setFlash(tcell.ColorAqua, "Starting "+action.Label+"…")
	go func() {
		result, err := v.tui.Select(v.ctx, action.Name, args)
		if err == nil {
			if session, ok := result.(consoleActionSession); ok {
				initial := session.InitialResult()
				v.queueUpdate(func() {
					v.busy = false
					v.busyAction = ""
					v.setFlash(tcell.ColorGreen, strings.ReplaceAll(summarizeInteractiveResult(initial), "\n", " · "))
				})
				err = session.Wait(v.ctx)
				if err != nil {
					v.queueUpdate(func() {
						v.setFlash(tcell.ColorOrangeRed, "Debugger session ended: "+err.Error())
					})
				} else {
					v.queueUpdate(func() {
						v.setFlash(tcell.ColorLightGray, "Debugger session disconnected")
					})
				}
				return
			}
		}
		message := ""
		color := tcell.ColorGreen
		if err != nil {
			message = "Error: " + err.Error()
			color = tcell.ColorOrangeRed
		} else {
			message = summarizeInteractiveResult(result)
		}
		v.queueUpdate(func() {
			v.busy = false
			v.busyAction = ""
			v.setFlash(color, strings.ReplaceAll(message, "\n", " · "))
		})
	}()
}

func (v *applicationTUI) openPrompt(mode string) {
	v.promptMode = mode
	v.prompt.SetText("")
	if mode == "filter" {
		v.prompt.SetLabel("/ ")
	} else {
		v.prompt.SetLabel(": ")
	}
	v.root.ResizeItem(v.prompt, 1, 0)
	v.app.SetFocus(v.prompt)
}

func (v *applicationTUI) closePrompt() {
	v.promptMode = ""
	v.prompt.SetText("")
	v.root.ResizeItem(v.prompt, 0, 0)
	v.app.SetFocus(v.table)
}

func (v *applicationTUI) promptChanged(text string) {
	if v.promptMode == "filter" {
		v.rebuildActions(text)
	}
}

func (v *applicationTUI) promptDone(key tcell.Key) {
	if key == tcell.KeyEscape {
		if v.promptMode == "filter" {
			v.rebuildActions("")
		}
		v.closePrompt()
		return
	}
	if key != tcell.KeyEnter {
		return
	}
	mode, value := v.promptMode, strings.TrimSpace(v.prompt.GetText())
	v.closePrompt()
	if mode == "filter" || value == "" {
		return
	}
	action, args, ok := resolveTUISelection(value)
	if !ok {
		return
	}
	if action == "q" || action == "quit" {
		v.shutdown()
		return
	}
	v.activateActionName(action, args)
}

func (v *applicationTUI) showArgumentDialog(
	action console.Action,
	title, label, initial string,
	arguments func(string) []string,
) {
	input := tview.NewInputField().SetLabel(label + ": ").SetText(initial)
	form := tview.NewForm()
	form.AddFormItem(input)
	form.SetButtonsAlign(tview.AlignCenter)
	form.SetBorder(true)
	form.SetTitle(" [::b]" + title + "[-:-:-] ")
	form.SetBorderColor(tcell.ColorAqua)
	submit := func() {
		value := strings.TrimSpace(input.GetText())
		if value == "" {
			v.setFlash(tcell.ColorOrangeRed, label+" is required")
			return
		}
		v.closeDialog()
		v.startAction(action, arguments(value))
	}
	form.AddButton("Run", submit)
	form.AddButton("Cancel", v.closeDialog)
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			v.closeDialog()
			return nil
		}
		return event
	})
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			submit()
		} else if key == tcell.KeyEscape {
			v.closeDialog()
		}
	})
	v.dialogOpen = true
	v.pages.AddPage(applicationTUIDialogPage, centeredPrimitive(form, 72, 9), true, true)
	v.app.SetFocus(input)
}

func (v *applicationTUI) showHelp() {
	modal := tview.NewModal()
	modal.SetText(strings.Join([]string{
		"Grove Shop navigation",
		"",
		"↑/↓ or j/k   Move selection",
		"Enter/→      Run selected action",
		":            Command mode",
		"/            Filter actions",
		"g / G        First / last action",
		"Esc          Close or clear filter",
		"q            Quit",
	}, "\n"))
	modal.AddButtons([]string{"Close"})
	modal.SetDoneFunc(func(int, string) { v.closeDialog() })
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || (event.Key() == tcell.KeyRune && event.Rune() == '?') {
			v.closeDialog()
			return nil
		}
		return event
	})
	v.dialogOpen = true
	v.pages.AddPage(applicationTUIDialogPage, modal, false, true)
	v.app.SetFocus(modal)
}

func (v *applicationTUI) showLogs() {
	if v.logsOpen {
		return
	}
	logsCtx, cancel := context.WithCancel(v.ctx)
	view := tview.NewTextView()
	view.SetDynamicColors(true)
	view.SetScrollable(true)
	view.SetWrap(false)
	view.SetBorder(true)
	view.SetBorderColor(tcell.ColorDarkCyan)
	view.SetBorderFocusColor(tcell.ColorAqua)
	view.SetTitle(" [::b]Logs and health diagnostics[-:-:-] ")
	view.SetText("[aqua::b]Loading application, cluster, and System NATS diagnostics…[-:-:-]")
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || (event.Key() == tcell.KeyRune && event.Rune() == 'q') {
			v.closeLogs()
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'r' {
			go v.refreshLogs(logsCtx)
			return nil
		}
		return event
	})
	v.logsView = view
	v.logsOpen = true
	v.logsCancel = cancel
	v.pages.AddPage(applicationTUILogsPage, view, true, true)
	v.app.SetFocus(view)
	go v.watchLogs(logsCtx)
}

func (v *applicationTUI) watchLogs(ctx context.Context) {
	ticker := time.NewTicker(applicationLogsRefreshInterval)
	defer ticker.Stop()
	for {
		v.refreshLogs(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (v *applicationTUI) refreshLogs(ctx context.Context) {
	attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	result, err := v.tui.Select(attemptCtx, "logs.view", nil)
	cancel()
	text := ""
	if err != nil {
		text = "[orangered::b]Unable to read logs[-:-:-]\n" + tview.Escape(err.Error())
	} else if logs, ok := result.(applicationLogsView); ok {
		text = renderApplicationLogs(logs)
	} else {
		text = "[orangered::b]Logs action returned an unexpected result[-:-:-]"
	}
	v.queueUpdate(func() {
		if !v.logsOpen || v.logsView == nil {
			return
		}
		row, column := v.logsView.GetScrollOffset()
		v.logsView.SetText(text)
		v.logsView.ScrollTo(row, column)
	})
}

func (v *applicationTUI) closeLogs() {
	if v.logsCancel != nil {
		v.logsCancel()
	}
	v.pages.RemovePage(applicationTUILogsPage)
	v.logsView = nil
	v.logsOpen = false
	v.logsCancel = nil
	v.app.SetFocus(v.table)
}

func renderApplicationLogs(logs applicationLogsView) string {
	var output strings.Builder
	healthColor := "green"
	if logs.Health != "healthy" {
		healthColor = "orangered"
	}
	fmt.Fprintf(
		&output,
		"[::b]HEALTH[-:-:-] [%s::b]%s[-:-:-]    [gray]auto-refresh 500ms · ↑/↓ scroll · r refresh · q/esc back[-:-:-]\n\n",
		healthColor,
		strings.ToUpper(displayTUIValue(logs.Health)),
	)
	if len(logs.Causes) == 0 {
		output.WriteString("[green::b]WHY HEALTHY[-:-:-]\n  No active health failures.\n")
	} else if logs.Health == "healthy" {
		output.WriteString("[orange::b]RECENT FAILURE / RECOVERY[-:-:-]\n")
		for _, cause := range logs.Causes {
			fmt.Fprintf(&output, "  [orange]•[-] %s\n", tview.Escape(cause))
		}
	} else {
		output.WriteString("[orangered::b]WHY NOT HEALTHY[-:-:-]\n")
		for _, cause := range logs.Causes {
			fmt.Fprintf(&output, "  [orangered]•[-] %s\n", tview.Escape(cause))
		}
	}
	var debugLines []string
	for _, session := range logs.DebugSessions {
		debugLines = append(debugLines, fmt.Sprintf(
			"service=%s node=%s worker=%s dap=%s",
			session.ServiceName, session.NodeID, session.WorkerID, session.DAPEndpoint,
		))
	}
	writeApplicationLogSection(&output, "ACTIVE DELVE / DAP SESSIONS", debugLines)
	writeApplicationLogSection(&output, "APPLICATION", logs.Application)
	writeApplicationLogSection(&output, "CLUSTER", logs.Cluster)
	writeApplicationLogSection(&output, "SYSTEM NATS / CONTROL PLANE", logs.SystemNATS)
	return output.String()
}

func writeApplicationLogSection(output *strings.Builder, title string, lines []string) {
	fmt.Fprintf(output, "\n[aqua::b]%s[-:-:-]\n", title)
	if len(lines) == 0 {
		output.WriteString("  [gray]No entries captured.[-:-:-]\n")
		return
	}
	for _, line := range lines {
		fmt.Fprintf(output, "  %s\n", tview.Escape(line))
	}
}

func (v *applicationTUI) closeDialog() {
	v.pages.RemovePage(applicationTUIDialogPage)
	v.dialogOpen = false
	v.app.SetFocus(v.table)
}

func (v *applicationTUI) setFlash(color tcell.Color, message string) {
	v.flash.SetTextColor(color)
	v.flash.SetText(message)
}

func (v *applicationTUI) shutdown() {
	v.cancel()
	v.app.Stop()
}

func displayTUIValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func centeredPrimitive(primitive tview.Primitive, width, height int) tview.Primitive {
	rows := tview.NewFlex().SetDirection(tview.FlexRow)
	rows.AddItem(nil, 0, 1, false)
	rows.AddItem(primitive, height, 0, true)
	rows.AddItem(nil, 0, 1, false)
	columns := tview.NewFlex()
	columns.AddItem(nil, 0, 1, false)
	columns.AddItem(rows, width, 0, true)
	columns.AddItem(nil, 0, 1, false)
	return columns
}

func hotkeyLabel(action string) string {
	for key, name := range map[rune]string{
		'd': "rollout.start",
		's': "cluster.status",
		'R': "cluster.restart",
		'x': "resilience.run",
		'D': "debug.demo.start",
		'a': "debug.attach",
		'l': "logs.view",
		'v': "app.orders.verify",
	} {
		if action == name {
			return string(key)
		}
	}
	return ""
}

func actionForHotkey(key rune) (string, bool) {
	action, ok := map[rune]string{
		'd': "rollout.start",
		's': "cluster.status",
		'R': "cluster.restart",
		'x': "resilience.run",
		'D': "debug.demo.start",
		'a': "debug.attach",
		'l': "logs.view",
		'v': "app.orders.verify",
	}[key]
	return action, ok
}

func k9sHintLine() string {
	return "[aqua::b]<enter>[-:-:-] Run  [aqua::b]<d>[-:-:-] Deploy  [aqua::b]<s>[-:-:-] Status  " +
		"[aqua::b]<x>[-:-:-] Resilience  [aqua::b]<R>[-:-:-] Restart  [aqua::b]<l>[-:-:-] Logs\n" +
		"[aqua::b]<:>[-:-:-] Command  [aqua::b]</>[-:-:-] Filter  [aqua::b]<?>[-:-:-] Help  " +
		"[aqua::b]<esc>[-:-:-] Back  [aqua::b]<q>[-:-:-] Quit"
}
