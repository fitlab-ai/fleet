package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
)

type pingResult struct {
	index int
	node  model.Node
	ms    int64
	ok    bool
}

func (a *App) Ping(target string) int {
	nodes := a.LoadNodes(true)
	targets := nodes
	if target != "" {
		node, err := model.ResolveNode(nodes, target)
		if err != nil {
			a.printf("%s\n", err)
			return 1
		}
		targets = []model.Node{node}
	}
	a.printf("TCP CONNECT only: checks the server port and does not verify the proxy protocol.\n")
	a.printf("Testing %d node(s)...\n%-4s %-38s %20s\n%s\n", len(targets), "", "NODE", "TCP CONNECT", "")
	results := make(chan pingResult, len(targets))
	var wg sync.WaitGroup
	for i, node := range targets {
		wg.Add(1)
		go func(index int, node model.Node) {
			defer wg.Done()
			start := time.Now()
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(node.Server, fmt.Sprint(node.Port)), 5*time.Second)
			if err == nil {
				_ = conn.Close()
			}
			results <- pingResult{index: index, node: node, ms: time.Since(start).Milliseconds(), ok: err == nil}
		}(i, node)
	}
	wg.Wait()
	close(results)
	var ordered []pingResult
	for result := range results {
		ordered = append(ordered, result)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })
	for _, result := range ordered {
		if result.ok {
			a.printf("   %-38s %10s (%dms)\n", result.node.Name, "REACHABLE", result.ms)
		} else {
			a.printf("   %-38s %20s\n", result.node.Name, "UNREACHABLE")
		}
	}
	a.printf("Use 'fleet health [node]' to verify the actual proxy protocol.\n")
	return 0
}

func (a *App) Health(target string) int {
	nodes := a.LoadNodes(true)
	targets := nodes
	if target != "" {
		node, err := model.ResolveNode(nodes, target)
		if err != nil {
			a.printf("%s\n", err)
			return 1
		}
		targets = []model.Node{node}
	}
	a.printf("PROXY HEALTH: verifies an HTTPS request through each Fleet outbound.\n")
	if a.DataPlanes == nil {
		a.printf("Data plane registry is not configured\n")
		return 1
	}
	failures := 0
	for _, node := range targets {
		status, elapsed := a.probeHealthDataPlane(node)
		if status != "HEALTHY" {
			failures++
		}
		if elapsed >= 0 {
			a.printf("  %-38s %s (%dms)\n", node.Name, status, elapsed)
		} else {
			a.printf("  %-38s %s\n", node.Name, status)
		}
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func (a *App) probeHealthDataPlane(node model.Node) (string, int64) {
	plane, err := a.DataPlanes.Configured(a.Config.Backend)
	if err != nil {
		return "DEPENDENCY_ERROR", -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	validate := dataplane.ValidateRequest{
		Backend: plane.ID(), Purpose: dataplane.ValidationProbe,
		Mode: dataplane.ModeProxy, Nodes: []model.Node{node},
	}
	capabilities, err := plane.Capabilities(ctx)
	if err != nil {
		return "DEPENDENCY_ERROR", -1
	}
	if err := capabilities.Require(validate); err != nil {
		return "CONFIG_ERROR", -1
	}
	if err := plane.Validate(ctx, validate); err != nil {
		if dataplane.IsCode(err, dataplane.CodeDependency) {
			return "DEPENDENCY_ERROR", -1
		}
		return "CONFIG_ERROR", -1
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, probeErr := plane.Probe(ctx, dataplane.ProbeRequest{
			Kind: dataplane.ProbeOutbound, Node: &node,
			Target: os.Getenv("FLEET_HEALTH_URL"),
		})
		if probeErr != nil {
			if dataplane.IsCode(probeErr, dataplane.CodeDependency) {
				return "DEPENDENCY_ERROR", -1
			}
			if dataplane.IsCode(probeErr, dataplane.CodeStart) && attempt == 0 {
				continue
			}
			return "START_FAILED", -1
		}
		elapsed := result.Latency.Milliseconds()
		if result.Status == dataplane.HealthHealthy {
			return "HEALTHY", elapsed
		}
		if result.Reason == dataplane.CodeStart && attempt == 0 {
			continue
		}
		if result.Reason == dataplane.CodeStart {
			return "START_FAILED", -1
		}
		return "UNHEALTHY", elapsed
	}
	return "START_FAILED", -1
}
