package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fitlab-ai/fleet/internal/backend"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/store"
)

func (a *App) findSingBox() int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,comm=,args=").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(result), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		if filepath.Base(parts[1]) == "sing-box" && strings.Contains(line, a.Config.Dir) {
			pid, _ := strconv.Atoi(parts[0])
			return pid
		}
	}
	return 0
}

func (a *App) portOwner() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := exec.CommandContext(ctx, "lsof", "-nP", fmt.Sprintf("-iTCP:%d", a.Config.Port), "-sTCP:LISTEN").Output()
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(result)))
	_ = scanner.Scan()
	if scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) >= 2 {
			return fmt.Sprintf("%s (PID %s)", parts[0], parts[1])
		}
	}
	return ""
}

func waitForPID(find func() int, fallback func() int, attempts int, interval time.Duration) int {
	for attempt := 0; attempt < attempts; attempt++ {
		if pid := find(); pid != 0 {
			return pid
		}
		if attempt+1 < attempts {
			time.Sleep(interval)
		}
	}
	return fallback()
}

func (a *App) Start(target, mode string) int {
	_ = a.stop(false)
	node, err := model.ResolveNode(a.LoadNodes(true), target)
	if err != nil {
		if target == "" {
			a.printf("Node required. Use 'fleet list' to choose a name or index.\n")
		} else {
			a.printf("%s\n", err)
		}
		return 1
	}
	if _, err := os.Stat(a.Config.SingBox); err != nil {
		a.printf("sing-box not found: %s\nInstall it with: brew install sing-box\n", a.Config.SingBox)
		return 1
	}
	singbox := backend.SingBox{Binary: a.Config.SingBox}
	if _, err := singbox.CheckVersion(); err != nil {
		a.printf("%s\n", err)
		return 1
	}
	if owner := a.portOwner(); owner != "" {
		a.printf("Port in use: 127.0.0.1:%d\nOwner: %s\n", a.Config.Port, owner)
		a.printf("Stop the owner first, or choose another port with:\n  FLEET_PORT=7891 fleet start <node>\n")
		return 1
	}
	var config map[string]any
	if mode == "tun" {
		config, err = backend.BuildTUNConfig(node, a.Config.Port)
		a.printf("Starting TUN mode with: %s\n(sudo may prompt for password)\n", node.Name)
	} else {
		config, err = backend.BuildProxyConfig(node, a.Config.Port)
		a.printf("Starting proxy mode with: %s\n", node.Name)
	}
	if err != nil {
		a.printf("%s\n", err)
		return 1
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	configPath := filepath.Join(a.Config.Dir, "sing-box.json")
	if err := store.AtomicWrite(configPath, append(data, '\n')); err != nil {
		a.printf("Config error: %s\n", err)
		return 1
	}
	env := os.Environ()
	if mode == "tun" {
		env = append(env, "ENABLE_DEPRECATED_LEGACY_DNS_SERVERS=true", "ENABLE_DEPRECATED_OUTBOUND_DNS_RULE_ITEM=true")
	}
	check := exec.Command(a.Config.SingBox, "check", "-c", configPath)
	check.Env = env
	if output, checkErr := check.CombinedOutput(); checkErr != nil {
		a.printf("Config error:\n%s\n", output)
		return 1
	}
	var snapshot map[string]any
	if mode == "proxy" {
		snapshot = a.proxySnapshot()
	}
	logPath := filepath.Join(a.Config.Dir, "sing-box.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		a.printf("✗ Failed to start %s mode\n", mode)
		return 1
	}
	args := []string{"run", "-c", configPath, "-D", a.Config.Dir}
	cmd := exec.Command(a.Config.SingBox, args...)
	if mode == "tun" && os.Geteuid() != 0 {
		cmd = exec.Command("sudo", append([]string{"-n", "env", "ENABLE_DEPRECATED_LEGACY_DNS_SERVERS=true", "ENABLE_DEPRECATED_OUTBOUND_DNS_RULE_ITEM=true", a.Config.SingBox}, args...)...)
	}
	cmd.Stdout, cmd.Stderr, cmd.Env = log, log, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		a.printf("✗ Failed to start %s mode\n", mode)
		return 1
	}
	_ = log.Close()
	pid := waitForPID(a.findSingBox, func() int {
		if cmd.Process == nil {
			return 0
		}
		if cmd.Process.Signal(syscall.Signal(0)) == nil {
			return cmd.Process.Pid
		}
		return 0
	}, 20, 200*time.Millisecond)
	if pid == 0 {
		a.printf("✗ Failed to start %s mode\n  Log: cat %s\n", mode, logPath)
		return 1
	}
	if mode == "proxy" {
		a.proxyEnable()
	}
	state := model.RuntimeState{
		Mode: mode, Node: node.Name, NodeName: node.Name, NodeKey: node.Fleet.NodeKey,
		SubscriptionID: node.Fleet.SubscriptionID, SubscriptionName: node.Fleet.SubscriptionName,
		PID: pid, Port: a.Config.Port, SystemProxyBefore: snapshot,
	}
	if state.NodeKey == "" {
		state.NodeKey = node.Name
	}
	_ = store.AtomicJSON(filepath.Join(a.Config.Dir, "state.json"), state)
	if mode == "tun" {
		a.printf("  HTTP/SOCKS: 127.0.0.1:%d\n  TUN: %s (system-wide)\n  System: macOS proxy unchanged\n  Docker containers: auto-proxied ✓\n", a.Config.Port, backend.TUNAddress)
	} else {
		a.printf("✓ proxy mode started (PID %d) — %s\n  HTTP/SOCKS: 127.0.0.1:%d\n  System: macOS proxy enabled\n", pid, node.Name, a.Config.Port)
	}
	return 0
}

func (a *App) stop(verbose bool) bool {
	state, _ := a.LoadState()
	pid := a.findSingBox()
	if pid == 0 && state != nil {
		pid = state.PID
	}
	if pid > 0 {
		process, _ := os.FindProcess(pid)
		_ = process.Signal(syscall.SIGTERM)
		time.Sleep(time.Second)
		if process.Signal(syscall.Signal(0)) == nil {
			_ = process.Signal(syscall.SIGKILL)
		}
	}
	if state != nil {
		a.proxyRestore(state.SystemProxyBefore)
	} else {
		a.proxyDisableOwn()
	}
	_ = os.Remove(filepath.Join(a.Config.Dir, "state.json"))
	if verbose {
		if state != nil || pid > 0 {
			a.printf("✓ Stopped and restored system proxy\n")
		} else {
			a.printf("System proxy cleaned\n")
		}
	}
	return state != nil || pid > 0
}

func (a *App) Stop() int { a.stop(true); return 0 }

func (a *App) Switch(target, mode string) int {
	if mode == "" {
		if state, _ := a.LoadState(); state != nil {
			mode = state.Mode
		} else {
			mode = "proxy"
		}
	}
	node, err := model.ResolveNode(a.LoadNodes(true), target)
	if err != nil {
		a.printf("%s\n", err)
		return 1
	}
	a.printf("Switching [%s] → %s\n", mode, node.Name)
	a.stop(false)
	if code := a.Start(target, mode); code != 0 {
		a.printf("✗ Failed\n")
		return 1
	}
	return 0
}

func (a *App) Status() int {
	state, _ := a.LoadState()
	pid := a.findSingBox()
	if pid > 0 && state != nil {
		a.printf("Mode:    %s\nStatus:  RUNNING (PID %d)\nNode:    %s\nPort:    127.0.0.1:%d (HTTP + SOCKS5)\n", state.Mode, pid, state.Node, a.Config.Port)
		a.printf("System:  macOS proxy %s\n", a.proxySummary())
		if state.Mode == "tun" {
			a.printf("TUN:     %s\n", backend.TUNAddress)
		}
	} else if pid > 0 {
		a.printf("Status: RUNNING (PID %d) — state missing\nSystem: macOS proxy %s\n", pid, a.proxySummary())
	} else {
		a.printf("Status: STOPPED\nSystem: macOS proxy %s\n", a.proxySummary())
		if state != nil {
			a.printf("Last:   %s (%s)\n", state.Node, state.Mode)
		}
	}
	return 0
}
