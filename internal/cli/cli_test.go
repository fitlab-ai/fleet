package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpPreservesCoreCommands(t *testing.T) {
	var out bytes.Buffer
	code := Run(nil, Dependencies{Out: &out})
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, command := range []string{"subscription add", "refresh", "proxy start", "tun start", "export tun"} {
		if !strings.Contains(out.String(), command) {
			t.Fatalf("help missing %q", command)
		}
	}
}

func TestUnknownCommandReturnsOne(t *testing.T) {
	var out bytes.Buffer
	code := Run([]string{"wat"}, Dependencies{Out: &out})
	if code != 1 || !strings.Contains(out.String(), "Unknown command: wat") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}
