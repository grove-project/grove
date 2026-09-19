package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/systemnats"
)

const applicationLogLineLimit = 250

type applicationLogsView struct {
	Health        string                 `json:"health"`
	Causes        []string               `json:"causes"`
	Application   []string               `json:"application"`
	Cluster       []string               `json:"cluster"`
	SystemNATS    []string               `json:"system_nats"`
	DebugSessions []console.DebugSession `json:"debug_sessions"`
}

type applicationNodeLogs struct {
	NodeID string
	Output string
}

func (c *applicationController) logs(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	status, statusErr := c.status(ctx)
	c.mu.RLock()
	cluster := c.cluster
	var systemNATSURL string
	var nodes []applicationNodeLogs
	var debugSessions []console.DebugSession
	if cluster != nil {
		systemNATSURL = cluster.systemNATSURL
		nodes = make([]applicationNodeLogs, len(cluster.nodes))
		for index, node := range cluster.nodes {
			nodes[index] = applicationNodeLogs{
				NodeID: fmt.Sprintf("node-%d", index+1),
				Output: node.Logs(),
			}
		}
	}
	debugSessions = copyDebugSessions(c.debugSessions)
	c.mu.RUnlock()
	return buildApplicationLogsView(status, statusErr, nodes, systemNATSURL, debugSessions), nil
}

func buildApplicationLogsView(
	status groveshop.ClusterStatusView,
	statusErr error,
	nodes []applicationNodeLogs,
	systemNATSURL string,
	debugSessions []console.DebugSession,
) applicationLogsView {
	view := applicationLogsView{
		Health:        status.Health,
		Causes:        []string{},
		Application:   []string{},
		Cluster:       []string{},
		SystemNATS:    []string{},
		DebugSessions: append([]console.DebugSession(nil), debugSessions...),
	}
	if view.Health == "" {
		view.Health = "unknown"
	}
	causes := make(map[string]struct{})
	addCause := func(cause string) {
		cause = strings.TrimSpace(cause)
		if cause == "" {
			return
		}
		if _, exists := causes[cause]; exists {
			return
		}
		causes[cause] = struct{}{}
		view.Causes = append(view.Causes, cause)
	}
	if statusErr != nil {
		addCause("cluster status unavailable: " + statusErr.Error())
	}
	if status.Health == "not-deployed" {
		addCause("Grove Shop has not been deployed")
	}
	view.Cluster = append(view.Cluster, fmt.Sprintf(
		"read-model ready=%t health=%s nodes=%d placements=%d",
		status.Ready,
		view.Health,
		len(status.Nodes),
		len(status.Placements),
	))
	for _, node := range status.Nodes {
		line := fmt.Sprintf("node=%s health=%s components=%d", node.NodeID, node.Health, len(node.Components))
		if node.Error != "" {
			line += " error=" + node.Error
		}
		view.Cluster = append(view.Cluster, line)
		if node.Health != string(systemnats.HealthHealthy) {
			cause := fmt.Sprintf("%s is %s", node.NodeID, node.Health)
			if node.Error != "" {
				cause += ": " + node.Error
			}
			addCause(cause)
		}
		for _, component := range node.Components {
			componentLine := fmt.Sprintf(
				"node=%s service=%s worker=%s state=%s",
				node.NodeID,
				component.Name,
				component.WorkerID,
				component.State,
			)
			if component.Error != "" {
				componentLine += " error=" + component.Error
			}
			view.Application = append(view.Application, componentLine)
			if component.State != string(systemnats.ComponentHealthy) && component.State != string(systemnats.ComponentDebugging) {
				cause := fmt.Sprintf("%s on %s is %s", component.Name, node.NodeID, component.State)
				if component.Error != "" {
					cause += ": " + component.Error
				}
				addCause(cause)
			}
		}
	}
	if systemNATSURL != "" {
		view.SystemNATS = append(view.SystemNATS, "client-endpoint="+systemNATSURL)
	}
	for _, placement := range status.Placements {
		view.SystemNATS = append(view.SystemNATS, fmt.Sprintf(
			"placement service=%s node=%s health=%s subject=%s",
			placement.Name,
			placement.NodeID,
			placement.Health,
			placement.InvocationSubject,
		))
		if placement.Health != string(systemnats.ComponentHealthy) && placement.Health != string(systemnats.ComponentDebugging) {
			addCause(fmt.Sprintf(
				"%s placement on %s is %s",
				placement.Name,
				placement.NodeID,
				placement.Health,
			))
		}
	}
	if status.Rollout != nil {
		view.Cluster = append(view.Cluster, fmt.Sprintf(
			"rollout generation=%d phase=%s",
			status.Rollout.Generation,
			status.Rollout.Phase,
		))
		if failure := status.Rollout.Failure; failure != nil {
			cause := fmt.Sprintf("rollout %s", failure.Code)
			if failure.Component != "" {
				cause += " component=" + failure.Component
			}
			if failure.Field != "" {
				cause += " field=" + failure.Field
			}
			if failure.Message != "" {
				cause += ": " + failure.Message
			}
			addCause(cause)
		}
	}
	for _, node := range nodes {
		classifyApplicationNodeLogs(node, &view)
	}
	view.Application = tailApplicationLogLines(view.Application, applicationLogLineLimit)
	view.Cluster = tailApplicationLogLines(view.Cluster, applicationLogLineLimit)
	view.SystemNATS = tailApplicationLogLines(view.SystemNATS, applicationLogLineLimit)
	return view
}

func classifyApplicationNodeLogs(node applicationNodeLogs, view *applicationLogsView) {
	for _, rawLine := range strings.Split(node.Output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		var event lifecycleEvent
		if json.Unmarshal([]byte(line), &event) == nil && event.Event != "" {
			view.Cluster = append(view.Cluster, fmt.Sprintf("node=%s event=%s", node.NodeID, event.Event))
			if event.SystemNATSURL != "" {
				view.SystemNATS = append(view.SystemNATS, fmt.Sprintf(
					"node=%s client=%s route=%s",
					node.NodeID,
					event.SystemNATSURL,
					displayApplicationLogValue(event.SystemNATSRouteURL),
				))
			}
			continue
		}
		entry := node.NodeID + " " + line
		lower := strings.ToLower(line)
		if strings.Contains(lower, "nats") || strings.Contains(lower, "jetstream") ||
			strings.Contains(lower, "raft") || strings.Contains(lower, "subject") {
			view.SystemNATS = append(view.SystemNATS, entry)
		} else {
			view.Application = append(view.Application, entry)
		}
	}
}

func displayApplicationLogValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func tailApplicationLogLines(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	return append([]string(nil), lines[len(lines)-limit:]...)
}
