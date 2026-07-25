package subscription

import (
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/model"
)

func TestValidateNodesProtocolsCountsAndDuplicates(t *testing.T) {
	nodes := []model.Node{
		{Name: "v", Type: "vmess", Server: "v.example", Port: 443, UUID: "id"},
		{Name: "h", Type: "hysteria2", Server: "h.example", Port: 443, Password: "p"},
		{Name: "a", Type: "anytls", Server: "a.example", Port: 443, Password: "p"},
		{Name: "t", Type: "trojan", Server: "t.example", Port: 443, Password: "p"},
	}
	got, counts, err := ValidateNodes(nodes)
	if err != nil || len(got) != 4 || counts["trojan"] != 1 {
		t.Fatalf("validate=%#v %#v %v", got, counts, err)
	}
	nodes[1].Name = "v"
	if _, _, err := ValidateNodes(nodes); err == nil {
		t.Fatal("duplicate names must fail")
	}
	nodes[1].Name, nodes[1].Type = "future", "future"
	if _, _, err := ValidateNodes(nodes); err == nil {
		t.Fatal("unknown protocol must fail")
	}
}

func TestValidateTrojanFields(t *testing.T) {
	valid := model.Node{
		Name: "trojan", Type: "trojan", Server: "example.com", Port: 443,
		Password: "secret", TLS: true, Network: "tcp", SNI: "sni.example",
	}
	if _, _, err := ValidateNodes([]model.Node{valid}); err != nil {
		t.Fatalf("valid Trojan rejected: %v", err)
	}
	for name, mutate := range map[string]func(*model.Node){
		"tls disabled":        func(node *model.Node) { node.TLS = false },
		"unsupported network": func(node *model.Node) { node.Network = "ws" },
		"transport options": func(node *model.Node) {
			node.Extra = map[string]any{"ws-opts": map[string]any{}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			node := valid
			mutate(&node)
			if _, _, err := ValidateNodes([]model.Node{node}); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid Trojan accepted or leaked secret: %v", err)
			}
		})
	}
}

func TestValidateHysteria2NormalizesAndRejectsFields(t *testing.T) {
	node := model.Node{
		Name: "h", Type: "hysteria2", Server: "example.com", Port: 443, Password: "secret",
		Ports: "443,1000-1002", HopInterval: 5, Obfs: "salamander", ObfsPassword: "obfs-secret",
	}
	nodes, _, err := ValidateNodes([]model.Node{node})
	if err != nil {
		t.Fatal(err)
	}
	internal := nodes[0].Extra["_fleet_hysteria2"].(map[string]any)
	ports := internal["server_ports"].([]string)
	if len(ports) != 2 || ports[0] != "443:443" || ports[1] != "1000:1002" || internal["hop_interval"] != "5s" {
		t.Fatalf("normalization mismatch: %#v", internal)
	}
	node.Extra = map[string]any{"mport": "443"}
	if _, _, err := ValidateNodes([]model.Node{node}); err == nil ||
		strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "obfs-secret") {
		t.Fatalf("unsupported field accepted or leaked secret: %v", err)
	}
}

func TestEnforceNodeCountCompatibility(t *testing.T) {
	if err := EnforceNodeCount(49, 100, false); err == nil {
		t.Fatal("unsafe node-count shrink accepted")
	}
	if err := EnforceNodeCount(49, 100, true); err != nil {
		t.Fatalf("force did not accept quantity-only shrink: %v", err)
	}
	if err := EnforceNodeCount(0, 100, true); err == nil {
		t.Fatal("force accepted an empty subscription")
	}
}

func TestValidateURLRequiresHTTPSAndNoUserInfo(t *testing.T) {
	for _, value := range []string{"http://example.com/sub", "https://u:p@example.com/sub", "not-a-url"} {
		if _, err := ValidateURL(value); err == nil {
			t.Fatalf("accepted unsafe URL %q", value)
		}
	}
	got, err := ValidateURL(" https://example.com/sub ")
	if err != nil || !strings.HasPrefix(got, "https://") {
		t.Fatalf("valid URL failed: %q %v", got, err)
	}
}
