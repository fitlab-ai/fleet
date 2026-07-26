package platform

import (
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
)

func TestParseProcessListUsesFullArgumentFingerprint(t *testing.T) {
	identities := ParseProcessList(
		"4242 sing-box /opt/homebrew/bin/sing-box run -c /tmp/fleet/sing-box.json -D /tmp/fleet\n",
	)
	if len(identities) != 1 {
		t.Fatalf("identities = %#v", identities)
	}
	wantArgs := []string{
		"/opt/homebrew/bin/sing-box", "run", "-c",
		"/tmp/fleet/sing-box.json", "-D", "/tmp/fleet",
	}
	if identities[0].PID != 4242 ||
		identities[0].ArgsFingerprint != FingerprintArgs(wantArgs) {
		t.Fatalf("identity = %#v", identities[0])
	}
}

func TestSameProcessRejectsPIDReuse(t *testing.T) {
	expected := dataplane.ProcessIdentity{
		PID: 42, Executable: "/opt/homebrew/bin/sing-box",
		ArgsFingerprint: FingerprintArgs([]string{"sing-box", "run", "-c", "fleet.json"}),
	}
	reused := dataplane.ProcessIdentity{
		PID: 42, Executable: "/opt/homebrew/bin/sing-box",
		ArgsFingerprint: FingerprintArgs([]string{"sing-box", "run", "-c", "other.json"}),
	}
	if SameProcess(reused, expected) {
		t.Fatal("PID reuse with different arguments was accepted")
	}
}
