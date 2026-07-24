package platform

import "testing"

func TestParseProxyOutput(t *testing.T) {
	got := ParseProxyOutput("Enabled: Yes\nServer: 127.0.0.1\nPort: 7890\n")
	if !got.Enabled || got.Server != "127.0.0.1" || got.Port != "7890" {
		t.Fatalf("parsed=%#v", got)
	}
}
