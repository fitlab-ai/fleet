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
