package platform

import "strings"

type ProxySetting struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    string `json:"port"`
}

func ParseProxyOutput(text string) ProxySetting {
	var out ProxySetting
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Enabled":
			out.Enabled = strings.TrimSpace(value) == "Yes"
		case "Server":
			out.Server = strings.TrimSpace(value)
		case "Port":
			out.Port = strings.TrimSpace(value)
		}
	}
	return out
}

type ProxyManager interface {
	Snapshot() map[string]map[string]ProxySetting
	Enable(host string, port int) bool
	Restore(map[string]map[string]ProxySetting) bool
	Summary() string
}

type NoopProxy struct{}

func (NoopProxy) Snapshot() map[string]map[string]ProxySetting {
	return map[string]map[string]ProxySetting{}
}
func (NoopProxy) Enable(string, int) bool                         { return true }
func (NoopProxy) Restore(map[string]map[string]ProxySetting) bool { return true }
func (NoopProxy) Summary() string                                 { return "OFF" }
