package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fitlab-ai/fleet/internal/backend"
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
	failures := 0
	for _, node := range targets {
		probe := a.healthProbe
		if probe == nil {
			probe = a.probeHealth
		}
		status, elapsed := probe(node)
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

func (a *App) probeHealth(node model.Node) (string, int64) {
	attempt := a.healthTry
	if attempt == nil {
		attempt = a.probeHealthAttempt
	}
	for try := 0; try < 2; try++ {
		status, elapsed, retry := attempt(node)
		if !retry {
			return status, elapsed
		}
	}
	return "START_FAILED", -1
}

func (a *App) probeHealthAttempt(node model.Node) (string, int64, bool) {
	if _, err := os.Stat(a.Config.SingBox); err != nil {
		return "DEPENDENCY_ERROR", -1, false
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "START_FAILED", -1, false
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	config, _ := backend.BuildProxyConfig(node, port)
	data, _ := json.Marshal(config)
	root, _ := os.MkdirTemp("", "fleet-health-")
	defer os.RemoveAll(root)
	path := filepath.Join(root, "config.json")
	writeFile := a.writeFile
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	if err := writeFile(path, data, 0o600); err != nil {
		return "CONFIG_ERROR", -1, false
	}
	log, _ := os.OpenFile(filepath.Join(root, "sing-box.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	cmd := exec.Command(a.Config.SingBox, "run", "-c", path, "-D", root)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return "START_FAILED", -1, false
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(time.Second):
		}
		_ = log.Close()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return "START_FAILED", -1, true
		default:
		}
		conn, connectErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if connectErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	target := os.Getenv("FLEET_HEALTH_URL")
	if target == "" {
		target = "https://api.github.com"
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	curl := exec.CommandContext(ctx, "curl", "--proxy", fmt.Sprintf("http://127.0.0.1:%d", port), "--noproxy", "", "--silent", "--output", "/dev/null", "--write-out", "%{http_code}", "--connect-timeout", "10", "--max-time", "10", target)
	output, err := curl.Output()
	elapsed := time.Since(start).Milliseconds()
	if err == nil && len(output) == 3 && output[0] >= '1' && output[0] <= '5' {
		return "HEALTHY", elapsed, false
	}
	return "UNHEALTHY", elapsed, false
}
