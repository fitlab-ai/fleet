package app

import (
	"context"
	"errors"
	"os"

	"github.com/fitlab-ai/fleet/internal/backend"
	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
	fleetruntime "github.com/fitlab-ai/fleet/internal/runtime"
)

func (a *App) requireRuntime() bool {
	if a.Runtime != nil {
		return true
	}
	a.printf("Runtime manager is not configured\n")
	return false
}

func (a *App) Start(target, mode string) int {
	if !a.requireRuntime() {
		return 1
	}
	return a.startManaged(target, mode, true)
}

func (a *App) Stop() int {
	if !a.requireRuntime() {
		return 1
	}
	stopped, err := a.Runtime.Stop(context.Background())
	if err != nil {
		a.printf("✗ Failed to stop: %s\n", err)
		return 1
	}
	if stopped {
		a.printf("✓ Stopped and restored system proxy\n")
	} else {
		a.printf("System proxy cleaned\n")
	}
	return 0
}

func (a *App) Switch(target, mode string) int {
	if !a.requireRuntime() {
		return 1
	}
	return a.switchManaged(target, mode)
}

func (a *App) Status() int {
	if !a.requireRuntime() {
		return 1
	}
	return a.statusManaged()
}

func (a *App) startManaged(target, mode string, stopExisting bool) int {
	ctx := context.Background()
	if stopExisting {
		if _, err := a.Runtime.Stop(ctx); err != nil {
			a.printf("Could not stop the running instance: %s\n", err)
			return 1
		}
	}
	node, err := model.ResolveNode(a.LoadNodes(true), target)
	if err != nil {
		if target == "" {
			a.printf("Node required. Use 'fleet list' to choose a name or index.\n")
		} else {
			a.printf("%s\n", err)
		}
		return 1
	}
	requestMode := dataplane.Mode(mode)
	if requestMode == dataplane.ModeTUN {
		a.printf("Starting TUN mode with: %s\n(sudo may prompt for password)\n", node.Name)
	} else {
		requestMode = dataplane.ModeProxy
		a.printf("Starting proxy mode with: %s\n", node.Name)
	}
	state, err := a.Runtime.Start(ctx, fleetruntime.StartInput{
		Node: node,
		NodeRef: fleetruntime.NodeRef{
			Name: node.Name, Key: node.Fleet.NodeKey,
			SubscriptionID:   node.Fleet.SubscriptionID,
			SubscriptionName: node.Fleet.SubscriptionName,
		},
		Mode: requestMode, Port: a.Config.Port,
	})
	if err != nil {
		a.printf("✗ Failed to start %s mode: %s\n", requestMode, err)
		return 1
	}
	a.printManagedStarted(state, node.Name, requestMode)
	return 0
}

func (a *App) printManagedStarted(
	state *fleetruntime.State,
	nodeName string,
	requestMode dataplane.Mode,
) {
	if requestMode == dataplane.ModeTUN {
		a.printf(
			"  HTTP/SOCKS: 127.0.0.1:%d\n  TUN: %s (system-wide)\n  System: macOS proxy unchanged\n  Docker containers: auto-proxied ✓\n",
			a.Config.Port, backend.TUNAddress,
		)
	} else {
		a.printf(
			"✓ proxy mode started (PID %d) — %s\n  HTTP/SOCKS: 127.0.0.1:%d\n  System: macOS proxy enabled\n",
			state.Instance.Process.PID, nodeName, a.Config.Port,
		)
	}
}

func (a *App) switchManaged(target, mode string) int {
	if mode == "" {
		state, _, err := a.Runtime.Status(context.Background())
		if err == nil {
			mode = string(state.Mode)
		} else {
			mode = string(dataplane.ModeProxy)
		}
	}
	node, err := model.ResolveNode(a.LoadNodes(true), target)
	if err != nil {
		a.printf("%s\n", err)
		return 1
	}
	a.printf("Switching [%s] → %s\n", mode, node.Name)
	requestMode := dataplane.Mode(mode)
	if requestMode != dataplane.ModeTUN {
		requestMode = dataplane.ModeProxy
	}
	state, err := a.Runtime.Switch(context.Background(), fleetruntime.StartInput{
		Node: node,
		NodeRef: fleetruntime.NodeRef{
			Name: node.Name, Key: node.Fleet.NodeKey,
			SubscriptionID:   node.Fleet.SubscriptionID,
			SubscriptionName: node.Fleet.SubscriptionName,
		},
		Mode: requestMode, Port: a.Config.Port,
	})
	if err != nil {
		a.printf("✗ Failed\n")
		return 1
	}
	a.printManagedStarted(state, node.Name, requestMode)
	return 0
}

func (a *App) statusManaged() int {
	state, health, err := a.Runtime.Status(context.Background())
	if errors.Is(err, os.ErrNotExist) {
		a.printf("Status: STOPPED\nSystem: macOS proxy %s\n", a.managedProxySummary())
		return 0
	}
	if err != nil {
		a.printf("Status: UNKNOWN\nError:  %s\nSystem: macOS proxy %s\n", err, a.managedProxySummary())
		return 0
	}
	status := "RUNNING"
	if health.Status != dataplane.HealthHealthy {
		status = "UNKNOWN"
	}
	a.printf(
		"Mode:    %s\nStatus:  %s (PID %d)\nNode:    %s\nPort:    127.0.0.1:%d (HTTP + SOCKS5)\n",
		state.Mode, status, state.Instance.Process.PID, state.Node.Name, state.Port,
	)
	a.printf("System:  macOS proxy %s\n", a.managedProxySummary())
	if state.Mode == dataplane.ModeTUN {
		a.printf("TUN:     %s\n", backend.TUNAddress)
	}
	return 0
}

func (a *App) managedProxySummary() string {
	if a.Runtime.Proxy == nil {
		return "OFF"
	}
	summary, err := a.Runtime.Proxy.Summary(context.Background())
	if err != nil {
		return "UNKNOWN"
	}
	return summary
}
