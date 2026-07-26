package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/platform"
)

func TestStateStoreLoadsLegacyRuntimeState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	data := []byte(`{
	  "mode": "proxy",
	  "node": "legacy-node",
	  "node_key": "source/node",
	  "pid": 4242,
	  "port": 7890,
	  "system_proxy_before": {"Wi-Fi": {"http": {"enabled": true}}}
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := NewStateStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Legacy || state.Schema != SchemaV1Legacy {
		t.Fatalf("legacy state = %#v", state)
	}
	if state.Mode != dataplane.ModeProxy || state.Node.Name != "legacy-node" ||
		state.Instance.Process.PID != 4242 || state.Port != 7890 {
		t.Fatalf("legacy fields not preserved: %#v", state)
	}
}

func TestStateStoreRoundTripsV2Securely(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	store := NewStateStore(path)
	want := &State{
		Schema: SchemaV2, Phase: PhaseActive, LeaseID: "lease-1",
		Mode: dataplane.ModeProxy, Node: NodeRef{Name: "node"},
		Port: 7890,
		Instance: dataplane.Instance{
			ID: "instance-1", Backend: "sing-box",
			Process: dataplane.ProcessIdentity{PID: 4242},
		},
		Claims: []Claim{{Kind: dataplane.ResourcePort, Key: "tcp:127.0.0.1:7890", Status: ClaimActive}},
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != SchemaV2 || got.LeaseID != want.LeaseID ||
		got.Instance.Process.PID != 4242 || got.Claims[0].Status != ClaimActive {
		t.Fatalf("round trip = %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
}

func TestStateStoreRejectsFutureSchemaForWrites(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte(`{"schema":99,"mode":"proxy","node":"future"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := NewStateStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if !state.ReadOnly || state.Schema != 99 {
		t.Fatalf("future state = %#v", state)
	}
	if err := NewStateStore(path).Save(state); !dataplane.IsCode(err, dataplane.CodeStateCorrupt) {
		t.Fatalf("save error = %v, want state-corrupt", err)
	}
}

func TestLegacyOwnershipRequiresStrictProcessMatch(t *testing.T) {
	state := &State{
		Schema: SchemaV1Legacy, Legacy: true,
		Instance: dataplane.Instance{
			Backend: "sing-box",
			Process: dataplane.ProcessIdentity{PID: 4242},
		},
	}
	inspector := fakeProcessInspector{
		identity: dataplane.ProcessIdentity{
			PID: 4242, Executable: "/usr/local/bin/other",
			ArgsFingerprint: platform.FingerprintArgs([]string{"other", "run"}),
		},
	}
	err := state.ReconcileLegacy(inspector, "/tmp/fleet", "/opt/homebrew/bin/sing-box")
	if !dataplane.IsCode(err, dataplane.CodeOwnershipUnknown) {
		t.Fatalf("reconcile error = %v, want ownership-unknown", err)
	}
}

type fakeProcessInspector struct {
	identity dataplane.ProcessIdentity
	err      error
}

func (f fakeProcessInspector) Inspect(_ int) (dataplane.ProcessIdentity, error) {
	return f.identity, f.err
}
func (fakeProcessInspector) Signal(dataplane.ProcessIdentity, os.Signal) error { return nil }

var _ platform.ProcessInspector = fakeProcessInspector{}
