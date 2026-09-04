package backend

import (
	"fmt"
	"net"
	"strings"

	"github.com/fitlab-ai/fleet/internal/model"
)

const Host = "127.0.0.1"
const TUNAddress = "172.19.0.1/30"

func boolValue(value any) bool {
	got, _ := value.(bool)
	return got
}

func outbound(node model.Node) (map[string]any, error) {
	return outboundForDial(node, node.Server)
}

func outboundForDial(node model.Node, dialServer string) (map[string]any, error) {
	if dialServer == "" {
		dialServer = node.Server
	}
	out := map[string]any{"tag": "proxy"}
	switch node.Type {
	case "vmess":
		alterID := 0
		if node.AlterID != nil {
			alterID = *node.AlterID
		}
		security := node.Cipher
		if security == "" {
			security = "auto"
		}
		out["type"], out["server"], out["server_port"] = "vmess", dialServer, node.Port
		out["uuid"], out["security"], out["alter_id"] = node.UUID, security, alterID
		if node.Network == "ws" {
			transport := map[string]any{"type": "ws", "path": "/"}
			if path, ok := node.WSOpts["path"].(string); ok && path != "" {
				transport["path"] = path
			}
			if headers, ok := node.WSOpts["headers"].(map[string]any); ok && len(headers) > 0 {
				transport["headers"] = headers
			}
			if path, ok := node.Extra["ws-path"].(string); ok && transport["path"] == "/" && path != "" {
				transport["path"] = path
			}
			headers := map[string]any{}
			if raw, ok := node.Extra["ws-headers"].(map[string]any); ok {
				for key, value := range raw {
					if _, ok := value.(string); ok {
						headers[key] = value
					}
				}
			}
			if raw, ok := node.WSOpts["headers"].(map[string]any); ok {
				for key, value := range raw {
					if _, ok := value.(string); ok {
						headers[key] = value
					}
				}
			}
			if dialServer != node.Server && net.ParseIP(node.Server) == nil &&
				!hasHeader(headers, "Host") {
				headers["Host"] = node.Server
			}
			if len(headers) > 0 {
				transport["headers"] = headers
			}
			out["transport"] = transport
		}
	case "hysteria2":
		serverName := node.SNI
		if serverName == "" {
			serverName = node.Server
		}
		out["type"], out["server"], out["password"] = "hysteria2", dialServer, node.Password
		out["tls"] = map[string]any{"enabled": true, "server_name": serverName, "insecure": boolValue(node.SkipCertVerify)}
		internal, _ := node.Extra["_fleet_hysteria2"].(map[string]any)
		if ports := internal["server_ports"]; ports != nil {
			out["server_ports"] = ports
		} else {
			out["server_port"] = node.Port
		}
		for _, field := range []string{"hop_interval", "obfs"} {
			if value := internal[field]; value != nil {
				out[field] = value
			}
		}
		if node.ALPN != nil {
			out["tls"].(map[string]any)["alpn"] = node.ALPN
		}
		if node.Down != nil {
			out["down_mbps"] = node.Down
		}
		if node.Up != nil {
			out["up_mbps"] = node.Up
		}
	case "anytls", "trojan":
		serverName := node.SNI
		if serverName == "" {
			serverName = node.Server
		}
		tls := map[string]any{"enabled": true, "server_name": serverName, "insecure": boolValue(node.SkipCertVerify)}
		if node.ALPN != nil {
			tls["alpn"] = node.ALPN
		}
		if fingerprint, ok := node.Extra["client-fingerprint"].(string); ok && fingerprint != "" && node.Type == "anytls" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
		}
		out["type"], out["server"], out["server_port"] = node.Type, dialServer, node.Port
		out["password"], out["tls"] = node.Password, tls
	default:
		return nil, model.NewError("protocol", "Unsupported proxy protocol", nil)
	}
	return out, nil
}

func hasHeader(headers map[string]any, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func BuildProxyConfig(node model.Node, port int) (map[string]any, error) {
	proxy, err := outbound(node)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"log":       map[string]any{"level": "warn"},
		"inbounds":  []any{map[string]any{"type": "mixed", "tag": "mixed-in", "listen": Host, "listen_port": port}},
		"outbounds": []any{proxy, map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{
			"rules":                 []any{map[string]any{"inbound": "mixed-in", "outbound": "proxy"}},
			"auto_detect_interface": true,
		},
	}, nil
}

func BuildTUNConfig(node model.Node, port int) (map[string]any, error) {
	return buildTUNConfig(node, port, nil)
}

func buildTUNConfig(node model.Node, port int, routeExclusions []string) (map[string]any, error) {
	return buildTUNConfigForDial(node, port, node.Server, routeExclusions)
}

func buildTUNConfigForDial(
	node model.Node,
	port int,
	dialServer string,
	routeExclusions []string,
) (map[string]any, error) {
	proxy, err := outboundForDial(node, dialServer)
	if err != nil {
		return nil, err
	}
	tunInbound := map[string]any{
		"type": "tun", "tag": "tun-in", "address": []any{TUNAddress},
		"mtu": 9000, "auto_route": true, "strict_route": true, "stack": "system",
	}
	if len(routeExclusions) > 0 {
		tunInbound["route_exclude_address"] = routeExclusions
	}
	return map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{
					"type": "https", "tag": "dns-remote", "server": "1.1.1.1",
					"path": "/dns-query", "detour": "proxy",
				},
				map[string]any{"type": "local", "tag": "dns-local", "prefer_go": true},
			},
			"final": "dns-remote", "strategy": "ipv4_only", "reverse_mapping": true,
		},
		"inbounds": []any{
			tunInbound,
			map[string]any{"type": "mixed", "tag": "mixed-in", "listen": Host, "listen_port": port},
		},
		"outbounds": []any{proxy, map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{
			"auto_detect_interface":   true,
			"default_domain_resolver": "dns-local",
			"rules": []any{
				map[string]any{"inbound": "tun-in", "outbound": "proxy"},
				map[string]any{"inbound": "mixed-in", "outbound": "proxy"},
			},
		},
	}, nil
}

func Export(node model.Node, mode string, port int) (map[string]any, error) {
	if mode == "tun" {
		return BuildTUNConfig(node, port)
	}
	if mode != "proxy" {
		return nil, fmt.Errorf("invalid mode: %s", mode)
	}
	return BuildProxyConfig(node, port)
}
