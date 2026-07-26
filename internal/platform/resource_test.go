package platform

import (
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
)

func TestParseLsofOwnerReturnsStableIdentity(t *testing.T) {
	owner, err := ParseLsofOwner("p4242\ncsing-box\na/opt/homebrew/bin/sing-box\na run\na-c\na/tmp/config.json\n")
	if err != nil {
		t.Fatal(err)
	}
	if owner.PID != 4242 || owner.Executable != "sing-box" || owner.ArgsFingerprint == "" {
		t.Fatalf("owner = %#v", owner)
	}
}

func TestFingerprintLinesIsOrderIndependent(t *testing.T) {
	first := FingerprintLines("route  one\nroute two\n")
	second := FingerprintLines(" route   two \nroute one\n")
	if len(first) != 2 || len(second) != 2 || first[0] != second[0] || first[1] != second[1] {
		t.Fatalf("fingerprints differ: %v vs %v", first, second)
	}
}

func TestNoopProxyNeverClaimsOwnership(t *testing.T) {
	proxy := NewProxyManager()
	owned, err := proxy.OwnedBy(t.Context(), "127.0.0.1", 7890)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("non-darwin noop proxy claimed system proxy ownership")
	}
}

var _ ResourceInspector = ExecResourceInspector{}
var _ = dataplane.ResourceDNS
