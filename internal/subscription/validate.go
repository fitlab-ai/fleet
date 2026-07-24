package subscription

import (
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/fitlab-ai/fleet/internal/model"
)

var supported = map[string]bool{"vmess": true, "hysteria2": true, "anytls": true, "trojan": true}

func ValidateURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", model.NewError("credential", "A valid HTTPS subscription URL is required", nil)
	}
	return value, nil
}

func ValidateNodes(nodes []model.Node) ([]model.Node, map[string]int, error) {
	if len(nodes) == 0 {
		return nil, nil, model.NewError("structure", "Subscription contains no proxy nodes", nil)
	}
	counts := map[string]int{"vmess": 0, "hysteria2": 0, "anytls": 0, "trojan": 0}
	names := map[string]bool{}
	out := make([]model.Node, 0, len(nodes))
	for _, node := range nodes {
		if strings.TrimSpace(node.Name) == "" || names[node.Name] {
			return nil, nil, model.NewError("node", "Proxy node names must be non-empty and unique", nil)
		}
		names[node.Name] = true
		if strings.TrimSpace(node.Server) == "" {
			return nil, nil, model.NewError("node", "A proxy node has an invalid server", nil)
		}
		if !supported[node.Type] {
			return nil, nil, model.NewError("protocol", "Subscription contains an unsupported protocol", nil)
		}
		if node.Port < 1 || node.Port > 65535 {
			return nil, nil, model.NewError("node", "A proxy node has an invalid port", nil)
		}
		if node.Type == "vmess" && strings.TrimSpace(node.UUID) == "" {
			return nil, nil, model.NewError("node", "A vmess node is missing required credentials", nil)
		}
		if node.Type != "vmess" && strings.TrimSpace(node.Password) == "" {
			return nil, nil, model.NewError("node", fmt.Sprintf("A %s node is missing required credentials", node.Type), nil)
		}
		if node.SkipCertVerify != nil && reflect.TypeOf(node.SkipCertVerify).Kind() != reflect.Bool {
			return nil, nil, model.NewError("node", "skip-cert-verify must be boolean", nil)
		}
		if node.Type == "trojan" {
			if node.SNI != "" && strings.TrimSpace(node.SNI) == "" {
				return nil, nil, model.NewError("node", "Trojan sni must be a non-empty string", nil)
			}
			if node.TLS != nil && node.TLS != true {
				return nil, nil, model.NewError("node", "Trojan tls must be enabled", nil)
			}
			if node.Network != "" && node.Network != "tcp" {
				return nil, nil, model.NewError("node", "Trojan only supports the tcp network", nil)
			}
			for _, field := range []string{"ws-opts", "grpc-opts", "http-opts", "h2-opts", "http-upgrade-opts", "reality-opts", "ech-opts", "client-fingerprint"} {
				if _, exists := node.Extra[field]; exists {
					return nil, nil, model.NewError("node", "Trojan transport options are not supported", nil)
				}
			}
		}
		if node.Type == "hysteria2" {
			if err := normalizeHysteria2(&node); err != nil {
				return nil, nil, err
			}
		}
		counts[node.Type]++
		out = append(out, node)
	}
	return out, counts, nil
}

func normalizeHysteria2(node *model.Node) error {
	for _, field := range []string{"mport", "name-cert-verify", "fingerprint", "realm-opts", "recv-window-conn", "recv-window", "disable-mtu-discovery", "fast-open", "max-idle-time", "keep-alive-period", "obfs-min-packet-size", "obfs-max-packet-size", "bbr-profile"} {
		if _, exists := node.Extra[field]; exists {
			return model.NewError("node", fmt.Sprintf("Hysteria2 field '%s' is not supported", field), nil)
		}
	}
	internal := map[string]any{}
	if value, ok := node.Ports.(string); ok && value != "" {
		var normalized []string
		for _, part := range strings.Split(value, ",") {
			m := regexp.MustCompile(`^([0-9]+)(?:-([0-9]+))?$`).FindStringSubmatch(strings.TrimSpace(part))
			if m == nil {
				return model.NewError("node", "Hysteria2 ports has invalid syntax", nil)
			}
			start, _ := strconv.Atoi(m[1])
			end := start
			if m[2] != "" {
				end, _ = strconv.Atoi(m[2])
			}
			if start < 1 || start > end || end > 65535 {
				return model.NewError("node", "Hysteria2 ports is outside the valid range", nil)
			}
			normalized = append(normalized, fmt.Sprintf("%d:%d", start, end))
		}
		internal["server_ports"] = normalized
	}
	if node.HopInterval != nil {
		text := fmt.Sprint(node.HopInterval)
		if !regexp.MustCompile(`^[0-9]+$`).MatchString(text) {
			return model.NewError("node", "Hysteria2 hop-interval has invalid syntax", nil)
		}
		seconds, _ := strconv.Atoi(text)
		if seconds < 1 {
			return model.NewError("node", "Hysteria2 hop-interval has invalid range", nil)
		}
		internal["hop_interval"] = fmt.Sprintf("%ds", seconds)
	}
	obfs, hasObfs := node.Obfs.(string)
	password, hasPassword := node.ObfsPassword.(string)
	if hasObfs != hasPassword || (hasObfs && (obfs != "salamander" || password == "")) {
		return model.NewError("node", "Hysteria2 obfs configuration is invalid", nil)
	}
	if hasObfs {
		internal["obfs"] = map[string]any{"type": obfs, "password": password}
	}
	if len(internal) > 0 {
		if node.Extra == nil {
			node.Extra = map[string]any{}
		}
		node.Extra["_fleet_hysteria2"] = internal
	}
	return nil
}

func EnforceNodeCount(current, previous int, force bool) error {
	if current < 1 {
		return model.NewError("quantity", "Subscription contains no valid nodes", nil)
	}
	if previous > 0 && float64(current) < float64(previous)*0.5 && !force {
		return model.NewError("quantity", fmt.Sprintf("Node count shrank from %d to %d; use --force to accept", previous, current), nil)
	}
	return nil
}
