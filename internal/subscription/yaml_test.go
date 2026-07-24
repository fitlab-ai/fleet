package subscription

import "testing"

func TestParseClashYAMLAndRejectTopLevelList(t *testing.T) {
	nodes, err := ParseYAML([]byte("proxies:\n  - name: v\n    type: vmess\n    server: example.com\n    port: 443\n    uuid: id\n"))
	if err != nil || len(nodes) != 1 || nodes[0].Name != "v" {
		t.Fatalf("parse=%#v %v", nodes, err)
	}
	if _, err := ParseYAML([]byte("- vmess://encoded\n")); err == nil {
		t.Fatal("top-level list must be rejected")
	}
}
