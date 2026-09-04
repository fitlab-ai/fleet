//go:build darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type NetworkSetupProxy struct{}

var proxyKinds = map[string][3]string{
	"http":  {"-getwebproxy", "-setwebproxy", "-setwebproxystate"},
	"https": {"-getsecurewebproxy", "-setsecurewebproxy", "-setsecurewebproxystate"},
	"socks": {"-getsocksfirewallproxy", "-setsocksfirewallproxy", "-setsocksfirewallproxystate"},
}

func NewProxyManager() ProxyManager { return NetworkSetupProxy{} }

func networkServices(ctx context.Context) ([]string, error) {
	output, err := exec.CommandContext(ctx, "networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, err
	}
	var services []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "An asterisk") && !strings.HasPrefix(line, "*") {
			services = append(services, line)
		}
	}
	return services, nil
}

func getProxySetting(ctx context.Context, service, kind string) (ProxySetting, error) {
	output, err := exec.CommandContext(ctx, "networksetup", proxyKinds[kind][0], service).Output()
	if err != nil {
		return ProxySetting{}, err
	}
	return ParseProxyOutput(string(output)), nil
}

func (NetworkSetupProxy) Snapshot(ctx context.Context) (ProxySnapshot, error) {
	services, err := networkServices(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := ProxySnapshot{}
	for _, service := range services {
		snapshot[service] = map[string]ProxySetting{}
		for kind := range proxyKinds {
			setting, err := getProxySetting(ctx, service, kind)
			if err != nil {
				return nil, err
			}
			snapshot[service][kind] = setting
		}
	}
	return snapshot, nil
}

func (NetworkSetupProxy) Enable(ctx context.Context, host string, port int) error {
	services, err := networkServices(ctx)
	if err != nil {
		return err
	}
	for _, service := range services {
		for _, spec := range proxyKinds {
			if err := exec.CommandContext(ctx, "networksetup", spec[1], service, host, strconv.Itoa(port)).Run(); err != nil {
				return err
			}
			if err := exec.CommandContext(ctx, "networksetup", spec[2], service, "on").Run(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Restore reverts Fleet-owned proxy entries back to baseline. Only entries
// that are currently enabled and point at host:port are considered owned by
// Fleet and therefore reverted; anything the user changed independently during
// the session (for example a corporate proxy added while Fleet was running) is
// left untouched. Services that were captured at start but have since been
// removed are skipped because they no longer exist in the current snapshot.
// A failure on one service does not abort the remaining restores.
func (NetworkSetupProxy) Restore(
	ctx context.Context,
	snapshot ProxySnapshot,
	host string,
	port int,
) error {
	current, err := (NetworkSetupProxy{}).Snapshot(ctx)
	if err != nil {
		return err
	}
	wantPort := strconv.Itoa(port)
	services := make([]string, 0, len(current))
	for service := range current {
		services = append(services, service)
	}
	sort.Strings(services)
	var firstErr error
	for _, service := range services {
		kinds := make([]string, 0, len(current[service]))
		for kind := range current[service] {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			spec, exists := proxyKinds[kind]
			if !exists {
				continue
			}
			setting := current[service][kind]
			if !setting.Enabled || setting.Server != host || setting.Port != wantPort {
				continue
			}
			baseline := snapshot[service][kind]
			if baseline.Server != "" && baseline.Port != "" && baseline.Port != "0" {
				if err := exec.CommandContext(ctx, "networksetup", spec[1], service, baseline.Server, baseline.Port).Run(); err != nil {
					firstErr = errors.Join(firstErr, err)
					continue
				}
			}
			state := "off"
			if baseline.Enabled {
				state = "on"
			}
			if err := exec.CommandContext(ctx, "networksetup", spec[2], service, state).Run(); err != nil {
				firstErr = errors.Join(firstErr, err)
			}
		}
	}
	return firstErr
}

func (NetworkSetupProxy) OwnedBy(ctx context.Context, host string, port int) (bool, error) {
	snapshot, err := (NetworkSetupProxy{}).Snapshot(ctx)
	if err != nil {
		return false, err
	}
	wantPort := strconv.Itoa(port)
	for _, settings := range snapshot {
		for _, setting := range settings {
			if setting.Enabled && setting.Server == host && setting.Port == wantPort {
				return true, nil
			}
		}
	}
	return false, nil
}

func (NetworkSetupProxy) Summary(ctx context.Context) (string, error) {
	snapshot, err := (NetworkSetupProxy{}).Snapshot(ctx)
	if err != nil {
		return "", err
	}
	var enabled []string
	for service, settings := range snapshot {
		for kind, setting := range settings {
			if setting.Enabled {
				enabled = append(enabled, fmt.Sprintf("%s/%s=%s:%s", service, kind, setting.Server, setting.Port))
			}
		}
	}
	if len(enabled) == 0 {
		return "OFF", nil
	}
	return "ON (" + strings.Join(enabled, ", ") + ")", nil
}
