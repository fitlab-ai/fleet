package platform

import (
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
)

func TestFindDescendantProcessFollowsSudoMonitorChain(t *testing.T) {
	args := []string{
		"/opt/homebrew/bin/sing-box", "run", "-c",
		"/tmp/fleet/sing-box.json", "-D", "/tmp/fleet",
	}
	expected := dataplane.ProcessIdentity{
		Executable:      args[0],
		ArgsFingerprint: FingerprintArgs(args),
	}
	output := `52422 100 sudo sudo -n env ENABLE_FEATURE=true /opt/homebrew/bin/sing-box run -c /tmp/fleet/sing-box.json -D /tmp/fleet
52424 52422 sudo sudo -n env ENABLE_FEATURE=true /opt/homebrew/bin/sing-box run -c /tmp/fleet/sing-box.json -D /tmp/fleet
52425 52424 sing-box /opt/homebrew/bin/sing-box run -c /tmp/fleet/sing-box.json -D /tmp/fleet
60000 100 sing-box /opt/homebrew/bin/sing-box run -c /tmp/fleet/sing-box.json -D /tmp/fleet
`
	identity, err := FindDescendantProcess(output, 52422, expected)
	if err != nil {
		t.Fatal(err)
	}
	if identity.PID != 52425 || identity.Executable != args[0] ||
		identity.ArgsFingerprint != expected.ArgsFingerprint {
		t.Fatalf("identity = %#v", identity)
	}
}

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
