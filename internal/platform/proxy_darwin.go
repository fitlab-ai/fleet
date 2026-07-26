//go:build darwin

package platform

import (
	"context"
	"fmt"
	"os/exec"
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

func (NetworkSetupProxy) Restore(ctx context.Context, snapshot ProxySnapshot) error {
	for service, settings := range snapshot {
		for kind, setting := range settings {
			spec, exists := proxyKinds[kind]
			if !exists {
				continue
			}
			if setting.Server != "" && setting.Port != "" && setting.Port != "0" {
				if err := exec.CommandContext(ctx, "networksetup", spec[1], service, setting.Server, setting.Port).Run(); err != nil {
					return err
				}
			}
			state := "off"
			if setting.Enabled {
				state = "on"
			}
			if err := exec.CommandContext(ctx, "networksetup", spec[2], service, state).Run(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (NetworkSetupProxy) OwnedBy(ctx context.Context, host string, port int) (bool, error) {
	snapshot, err := (NetworkSetupProxy{}).Snapshot(ctx)
	if err != nil {
		return false, err
	}
	wantPort := strconv.Itoa(port)
	found := false
	for _, settings := range snapshot {
		for _, setting := range settings {
			if setting.Enabled {
				found = true
				if setting.Server != host || setting.Port != wantPort {
					return false, nil
				}
			}
		}
	}
	return found, nil
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
