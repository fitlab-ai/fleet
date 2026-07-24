package app

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/fitlab-ai/fleet/internal/platform"
)

var proxyKinds = map[string][3]string{
	"http":  {"-getwebproxy", "-setwebproxy", "-setwebproxystate"},
	"https": {"-getsecurewebproxy", "-setsecurewebproxy", "-setsecurewebproxystate"},
	"socks": {"-getsocksfirewallproxy", "-setsocksfirewallproxy", "-setsocksfirewallproxystate"},
}

func networkServices() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	output, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var services []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "An asterisk") && !strings.HasPrefix(line, "*") {
			services = append(services, line)
		}
	}
	return services
}

func getSetting(service, kind string) platform.ProxySetting {
	output, err := exec.Command("networksetup", proxyKinds[kind][0], service).Output()
	if err != nil {
		return platform.ProxySetting{}
	}
	return platform.ParseProxyOutput(string(output))
}

func (a *App) proxySnapshot() map[string]any {
	out := map[string]any{}
	for _, service := range networkServices() {
		settings := map[string]any{}
		for kind := range proxyKinds {
			settings[kind] = getSetting(service, kind)
		}
		out[service] = settings
	}
	return out
}

func runNetworkSetup(args ...string) bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	return exec.Command("networksetup", args...).Run() == nil
}

func (a *App) proxyEnable() bool {
	ok := true
	for _, service := range networkServices() {
		for _, spec := range proxyKinds {
			ok = runNetworkSetup(spec[1], service, backendHost, strconv.Itoa(a.Config.Port)) && ok
			ok = runNetworkSetup(spec[2], service, "on") && ok
		}
	}
	return ok
}

const backendHost = "127.0.0.1"

func (a *App) proxyRestore(snapshot map[string]any) bool {
	if len(snapshot) == 0 {
		return a.proxyDisableOwn()
	}
	ok := true
	for service, raw := range snapshot {
		settings, _ := raw.(map[string]any)
		for kind, value := range settings {
			spec, exists := proxyKinds[kind]
			if !exists {
				continue
			}
			setting := platform.ProxySetting{}
			switch typed := value.(type) {
			case map[string]any:
				setting.Enabled, _ = typed["enabled"].(bool)
				setting.Server, _ = typed["server"].(string)
				setting.Port, _ = typed["port"].(string)
			case platform.ProxySetting:
				setting = typed
			}
			if setting.Server != "" && setting.Port != "" && setting.Port != "0" {
				ok = runNetworkSetup(spec[1], service, setting.Server, setting.Port) && ok
			}
			state := "off"
			if setting.Enabled {
				state = "on"
			}
			ok = runNetworkSetup(spec[2], service, state) && ok
		}
	}
	return ok
}

func (a *App) proxyDisableOwn() bool {
	ok := true
	for _, service := range networkServices() {
		for kind, spec := range proxyKinds {
			setting := getSetting(service, kind)
			if setting.Server == backendHost && setting.Port == strconv.Itoa(a.Config.Port) {
				ok = runNetworkSetup(spec[2], service, "off") && ok
			}
		}
	}
	return ok
}

func (a *App) proxySummary() string {
	var enabled []string
	for _, service := range networkServices() {
		for kind := range proxyKinds {
			setting := getSetting(service, kind)
			if setting.Enabled {
				enabled = append(enabled, fmt.Sprintf("%s/%s=%s:%s", service, kind, setting.Server, setting.Port))
			}
		}
	}
	if len(enabled) == 0 {
		return "OFF"
	}
	suffix := fmt.Sprintf("=%s:%d", backendHost, a.Config.Port)
	for _, item := range enabled {
		if !strings.HasSuffix(item, suffix) {
			return "ON (custom proxy settings)"
		}
	}
	return fmt.Sprintf("ON (%s:%d)", backendHost, a.Config.Port)
}
