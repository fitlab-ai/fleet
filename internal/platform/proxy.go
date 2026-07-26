package platform

import (
	"context"
	"strings"
)

type ProxySetting struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    string `json:"port"`
}

type ProxySnapshot map[string]map[string]ProxySetting

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
	Snapshot(context.Context) (ProxySnapshot, error)
	Enable(context.Context, string, int) error
	Restore(context.Context, ProxySnapshot) error
	OwnedBy(context.Context, string, int) (bool, error)
	Summary(context.Context) (string, error)
}

type NoopProxy struct{}

func (NoopProxy) Snapshot(context.Context) (ProxySnapshot, error) {
	return ProxySnapshot{}, nil
}
func (NoopProxy) Enable(context.Context, string, int) error { return nil }
func (NoopProxy) Restore(context.Context, ProxySnapshot) error {
	return nil
}
func (NoopProxy) OwnedBy(context.Context, string, int) (bool, error) { return false, nil }
func (NoopProxy) Summary(context.Context) (string, error)             { return "OFF", nil }
