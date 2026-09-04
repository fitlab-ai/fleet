package platform

import (
	"bytes"
	"context"
	"os"
	"strings"
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

func TestFingerprintArgsToleratesSpaceContainingPaths(t *testing.T) {
	args := []string{
		"/Users/John Doe/.local/bin/sing-box", "run", "-c",
		"/Users/John Doe/.config/fleet/runtime/sing-box.json", "-D",
		"/Users/John Doe/.config/fleet/runtime",
	}
	expected := FingerprintArgs(args)
	// ps loses argument boundaries, so it re-tokenizes the space-joined line
	// and the inspection side can only see the split tokens.
	tokenized := strings.Fields(strings.Join(args, " "))
	if FingerprintArgs(tokenized) != expected {
		t.Fatal("space-containing path changed the identity fingerprint")
	}
}

func TestExecLauncherOverridesExistingEnvironment(t *testing.T) {
	const key = "FLEET_OVERRIDE_TEST_KEY"
	previous, hadPrevious := os.LookupEnv(key)
	t.Cleanup(func() {
		if hadPrevious {
			os.Setenv(key, previous)
		} else {
			os.Unsetenv(key)
		}
	})
	if err := os.Setenv(key, "inherited"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	launcher := ExecLauncher{}
	handle, err := launcher.Start(context.Background(), LaunchRequest{
		Command:     []string{"/usr/bin/env"},
		Environment: map[string]string{key: "override"},
		Stdout:      &output,
		Stderr:      &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Wait(); err != nil {
		t.Fatal(err)
	}
	env := output.String()
	if strings.Count(env, key+"=") != 1 {
		t.Fatalf("environment contains duplicated key: %q", env)
	}
	if !strings.Contains(env, key+"=override\n") {
		t.Fatalf("inherited value was not overridden: %q", env)
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
