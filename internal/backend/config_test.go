package backend

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fitlab-ai/fleet/internal/model"
)

func TestProxyAndTUNShareTrojanOutbound(t *testing.T) {
	node := model.Node{Name: "t", Type: "trojan", Server: "example.com", Port: 443, Password: "secret", SNI: "sni.example"}
	proxy, err := BuildProxyConfig(node, 7890)
	if err != nil {
		t.Fatal(err)
	}
	tun, err := BuildTUNConfig(node, 7890)
	if err != nil {
		t.Fatal(err)
	}
	p := proxy["outbounds"].([]any)[0]
	q := tun["outbounds"].([]any)[0]
	pb, _ := json.Marshal(p)
	qb, _ := json.Marshal(q)
	if string(pb) != string(qb) {
		t.Fatalf("outbounds differ:\n%s\n%s", pb, qb)
	}
}

func TestHysteria2MapsConnectionOptions(t *testing.T) {
	node := model.Node{
		Name: "h", Type: "hysteria2", Server: "example.com", Port: 443, Password: "p",
		ALPN: []string{"h3"}, Up: 10, Down: 20,
		Extra: map[string]any{"_fleet_hysteria2": map[string]any{
			"server_ports": []string{"443:443", "1000:1002"},
			"hop_interval": "5s",
			"obfs":         map[string]any{"type": "salamander", "password": "o"},
		}},
	}
	cfg, err := BuildProxyConfig(node, 7890)
	if err != nil {
		t.Fatal(err)
	}
	out := cfg["outbounds"].([]any)[0].(map[string]any)
	if out["hop_interval"] != "5s" || out["up_mbps"] != 10 {
		t.Fatalf("missing options: %#v", out)
	}
}

func TestTUNConfigUsesCurrentDNSServerFormat(t *testing.T) {
	node := model.Node{
		Name: "t", Type: "trojan", Server: "example.com",
		Port: 443, Password: "secret", SNI: "example.com",
	}
	config, err := BuildTUNConfig(node, 7890)
	if err != nil {
		t.Fatal(err)
	}
	servers := config["dns"].(map[string]any)["servers"].([]any)
	want := []any{
		map[string]any{
			"type": "https", "tag": "dns-remote", "server": "1.1.1.1",
			"path": "/dns-query", "detour": "proxy",
		},
		map[string]any{"type": "local", "tag": "dns-local"},
	}
	gotJSON, _ := json.Marshal(servers)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("DNS servers:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

func TestTUNConfigAcceptedByInstalledSingBox(t *testing.T) {
	binary, err := exec.LookPath("sing-box")
	if err != nil {
		t.Skip("sing-box is not installed")
	}
	node := model.Node{
		Name: "t", Type: "trojan", Server: "example.com",
		Port: 443, Password: "secret", SNI: "example.com",
	}
	config, err := BuildTUNConfig(node, 7890)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check failed: %v\n%s", err, output)
	}
}
