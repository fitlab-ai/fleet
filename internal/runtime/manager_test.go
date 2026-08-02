package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
)

type fakePlane struct {
	id           dataplane.BackendID
	validateErr  error
	startErr     error
	probeErr     error
	cancelStart  context.CancelFunc
	validations  int
	starts       int
	stops        int
	stopErr      error
	stopCtxError error
}

func (f *fakePlane) ID() dataplane.BackendID { return f.id }
func (f *fakePlane) Capabilities(context.Context) (dataplane.Capabilities, error) {
	return dataplane.Capabilities{
		Modes:     []dataplane.Mode{dataplane.ModeProxy, dataplane.ModeTUN},
		Protocols: []string{"vmess"},
		Resources: map[dataplane.Mode][]dataplane.ResourceKind{
			dataplane.ModeProxy: {
				dataplane.ResourceRuntimeLock, dataplane.ResourceProcess,
				dataplane.ResourcePort, dataplane.ResourceSystemProxy,
			},
			dataplane.ModeTUN: {
				dataplane.ResourceRuntimeLock, dataplane.ResourceProcess,
				dataplane.ResourcePort, dataplane.ResourceTUN,
				dataplane.ResourceRoute, dataplane.ResourceDNS,
			},
		},
	}, nil
}
func (f *fakePlane) Validate(context.Context, dataplane.ValidateRequest) error {
	f.validations++
	return f.validateErr
}
func (f *fakePlane) Render(context.Context, dataplane.RenderRequest) (dataplane.ConfigArtifact, error) {
	return dataplane.ConfigArtifact{
		Backend: f.id, Format: "json", Filename: "config.json", Bytes: []byte(`{"ok":true}`),
	}, nil
}
func (f *fakePlane) Start(context.Context, dataplane.StartRequest) (dataplane.Instance, error) {
	f.starts++
	if f.cancelStart != nil {
		f.cancelStart()
	}
	return dataplane.Instance{
		ID: "instance-1", Backend: f.id, Mode: dataplane.ModeProxy,
		Process: dataplane.ProcessIdentity{
			PID: 4242, Executable: "/bin/fake", ArgsFingerprint: "args",
		},
	}, f.startErr
}
func (f *fakePlane) Stop(ctx context.Context, _ dataplane.Instance) error {
	f.stops++
	f.stopCtxError = ctx.Err()
	return f.stopErr
}
func (f *fakePlane) Probe(context.Context, dataplane.ProbeRequest) (dataplane.HealthResult, error) {
	return dataplane.HealthResult{Status: dataplane.HealthHealthy}, f.probeErr
}

type fakeProxy struct {
	snapshot platform.ProxySnapshot
	owned    bool
	enabled  int
	restored int
}

func (f *fakeProxy) Snapshot(context.Context) (platform.ProxySnapshot, error) {
	return f.snapshot, nil
}
func (f *fakeProxy) Enable(context.Context, string, int) error {
	f.enabled++
	f.owned = true
	return nil
}
func (f *fakeProxy) Restore(context.Context, platform.ProxySnapshot) error {
	f.restored++
	return nil
}
func (f *fakeProxy) OwnedBy(context.Context, string, int) (bool, error) {
	return f.owned, nil
}
func (f *fakeProxy) Summary(context.Context) (string, error) { return "OFF", nil }

type fakeResources struct{ ownerErr error }

func (f fakeResources) PortOwner(context.Context, dataplane.ListenEndpoint) (dataplane.ProcessIdentity, error) {
	return dataplane.ProcessIdentity{}, f.ownerErr
}
func (fakeResources) Snapshot(context.Context, []dataplane.ResourceKind) (platform.ResourceSnapshot, error) {
	return platform.ResourceSnapshot{}, nil
}

type scriptedResources struct {
	snapshots       []platform.ResourceSnapshot
	next            int
	legacySnapshots []platform.ResourceSnapshot
	legacyNext      int
}

func (s *scriptedResources) PortOwner(
	context.Context,
	dataplane.ListenEndpoint,
) (dataplane.ProcessIdentity, error) {
	return dataplane.ProcessIdentity{}, os.ErrNotExist
}

func (s *scriptedResources) Snapshot(
	context.Context,
	[]dataplane.ResourceKind,
) (platform.ResourceSnapshot, error) {
	if s.next >= len(s.snapshots) {
		return nil, errors.New("unexpected resource snapshot")
	}
	snapshot := s.snapshots[s.next]
	s.next++
	return snapshot, nil
}

func (s *scriptedResources) SnapshotLegacy(
	context.Context,
	[]dataplane.ResourceKind,
) (platform.ResourceSnapshot, error) {
	if s.legacyNext >= len(s.legacySnapshots) {
		return nil, errors.New("unexpected legacy resource snapshot")
	}
	snapshot := s.legacySnapshots[s.legacyNext]
	s.legacyNext++
	return snapshot, nil
}

func newTestManager(t *testing.T, configured dataplane.BackendID, planes ...dataplane.DataPlane) *Manager {
	t.Helper()
	registry, err := dataplane.NewRegistry("sing-box", planes...)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	return &Manager{
		Registry: registry, Store: NewStateStore(filepath.Join(root, "state.json")),
		RuntimeDir: root, Configured: configured,
		Proxy: &fakeProxy{}, Resources: fakeResources{ownerErr: os.ErrNotExist},
		CleanupTimeout: time.Second,
	}
}

func TestManagerStartsOnlyConfiguredDataPlane(t *testing.T) {
	singBox := &fakePlane{id: "sing-box"}
	mihomo := &fakePlane{id: "mihomo"}
	manager := newTestManager(t, "mihomo", singBox, mihomo)

	state, err := manager.Start(t.Context(), StartInput{
		Node: model.Node{Name: "node", Type: "vmess"},
		Mode: dataplane.ModeProxy, Port: 7890,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseActive || state.Instance.Backend != "mihomo" {
		t.Fatalf("state = %#v", state)
	}
	if mihomo.validations != 1 || mihomo.starts != 1 {
		t.Fatalf("mihomo calls validate=%d start=%d", mihomo.validations, mihomo.starts)
	}
	if singBox.validations != 0 || singBox.starts != 0 {
		t.Fatalf("non-configured sing-box was called: validate=%d start=%d", singBox.validations, singBox.starts)
	}
}

func TestManagerCancellationAfterStartUsesCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	plane := &fakePlane{id: "sing-box", cancelStart: cancel}
	manager := newTestManager(t, "", plane)

	if _, err := manager.Start(ctx, StartInput{
		Node: model.Node{Name: "node", Type: "vmess"},
		Mode: dataplane.ModeProxy, Port: 7890,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("start error = %v, want canceled", err)
	}
	if plane.stops != 1 {
		t.Fatalf("cleanup stops = %d, want 1", plane.stops)
	}
	if plane.stopCtxError != nil {
		t.Fatalf("cleanup inherited canceled context: %v", plane.stopCtxError)
	}
	if _, err := manager.Store.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state remains after successful cleanup: %v", err)
	}
}

func TestManagerDoesNotRestoreExternallyChangedProxy(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	proxy := manager.Proxy.(*fakeProxy)
	if _, err := manager.Start(t.Context(), StartInput{
		Node: model.Node{Name: "node", Type: "vmess"},
		Mode: dataplane.ModeProxy, Port: 7890,
	}); err != nil {
		t.Fatal(err)
	}
	proxy.owned = false

	if _, err := manager.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if proxy.restored != 0 {
		t.Fatalf("external proxy setting was overwritten: restores=%d", proxy.restored)
	}
}

func TestManagerValidationFailureDoesNotCreateState(t *testing.T) {
	plane := &fakePlane{
		id:          "sing-box",
		validateErr: dataplane.NewError(dataplane.CodeUnsupported, "validate", "sing-box", "unsupported", nil),
	}
	manager := newTestManager(t, "", plane)
	if _, err := manager.Start(t.Context(), StartInput{
		Node: model.Node{Name: "node", Type: "vmess"},
		Mode: dataplane.ModeProxy, Port: 7890,
	}); !dataplane.IsCode(err, dataplane.CodeUnsupported) {
		t.Fatalf("start error = %v", err)
	}
	if plane.starts != 0 {
		t.Fatal("adapter started after validation failure")
	}
	if _, err := manager.Store.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state created after validation failure: %v", err)
	}
}

func TestManagerRefusesUnverifiedLegacyStop(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	if err := os.WriteFile(
		manager.Store.Path,
		[]byte(`{"mode":"proxy","node":"legacy","pid":4242,"port":7890}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Stop(t.Context()); !dataplane.IsCode(err, dataplane.CodeOwnershipUnknown) {
		t.Fatalf("stop error = %v, want ownership-unknown", err)
	}
	if plane.stops != 0 {
		t.Fatal("unverified legacy process was stopped")
	}
}

func TestManagerPreservesDegradedStateWhenTUNStopAuthorizationIsDenied(t *testing.T) {
	permissionErr := dataplane.NewError(
		dataplane.CodePermission, "stop", "sing-box",
		"Could not obtain administrator authorization", errors.New("denied"),
	)
	plane := &fakePlane{id: "sing-box", stopErr: permissionErr}
	manager := newTestManager(t, "", plane)
	state := &State{
		Schema: SchemaV2, Phase: PhaseActive,
		LeaseID: "0123456789abcdef01234567", Mode: dataplane.ModeTUN,
		Instance: dataplane.Instance{
			Backend: "sing-box", Mode: dataplane.ModeTUN,
			Process: dataplane.ProcessIdentity{PID: 5252, Executable: "/bin/sing-box"},
		},
		Claims: []Claim{{
			Kind: dataplane.ResourceProcess, Owner: "0123456789abcdef01234567",
			Status: ClaimActive,
		}},
	}
	if err := manager.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	if stopped, err := manager.Stop(t.Context()); stopped || !dataplane.IsCode(err, dataplane.CodePermission) {
		t.Fatalf("stop = %v, %v; want preserved permission failure", stopped, err)
	}
	got, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != PhaseDegraded || got.LastError != permissionErr.Error() || len(got.Claims) != 1 {
		t.Fatalf("state = %#v, want degraded state with active claim", got)
	}
}

func TestManagerSwitchRunsUnderOneRuntimeLock(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	input := StartInput{
		Node: model.Node{Name: "first", Type: "vmess"},
		Mode: dataplane.ModeProxy, Port: 7890,
	}
	if _, err := manager.Start(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	input.Node.Name = "second"
	state, err := manager.Switch(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if state.Node.Name != "second" || plane.starts != 2 || plane.stops != 1 {
		t.Fatalf("state=%#v starts=%d stops=%d", state, plane.starts, plane.stops)
	}
}

func TestManagerRollsBackWhenTUNResourcesAreNotEstablished(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	if _, err := manager.Start(t.Context(), StartInput{
		Node: model.Node{Name: "node", Type: "vmess"},
		Mode: dataplane.ModeTUN, Port: 7890,
	}); !dataplane.IsCode(err, dataplane.CodeProbe) {
		t.Fatalf("start error = %v, want probe", err)
	}
	if plane.stops != 1 {
		t.Fatalf("cleanup stops = %d, want 1", plane.stops)
	}
	if _, err := manager.Store.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state remains after resource rollback: %v", err)
	}
}

func TestManagerStopIgnoresExternalNetworkResourceChanges(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	manager.Resources = &scriptedResources{snapshots: []platform.ResourceSnapshot{
		{
			dataplane.ResourceTUN:   {"if-lo0", "if-en0"},
			dataplane.ResourceRoute: {"if-en0:route-default", "if-en0:route-old-external"},
			dataplane.ResourceDNS:   {"dns-baseline"},
		},
		{
			dataplane.ResourceTUN:   {"if-lo0", "if-en0", "if-utun9"},
			dataplane.ResourceRoute: {"if-en0:route-default", "if-en0:route-old-external", "if-bridge100:route-new-external", "if-utun9:route-fleet"},
			dataplane.ResourceDNS:   {"dns-baseline"},
		},
		{
			dataplane.ResourceTUN:   {"if-lo0", "if-en0", "if-utun10"},
			dataplane.ResourceRoute: {"if-en0:route-default", "if-bridge100:route-new-external"},
			dataplane.ResourceDNS:   {"dns-baseline"},
		},
	}}

	if _, err := manager.Start(t.Context(), StartInput{
		Node: model.Node{Name: "node", Type: "vmess"},
		Mode: dataplane.ModeTUN, Port: 7890,
	}); err != nil {
		t.Fatal(err)
	}
	if stopped, err := manager.Stop(t.Context()); err != nil || !stopped {
		t.Fatalf("stop = %v, %v; want successful cleanup despite external changes", stopped, err)
	}
}

func TestManagerStopUsesLegacySnapshotForV2StateWithoutOwnedResources(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	legacyBaseline := platform.ResourceSnapshot{
		dataplane.ResourceTUN:   {"legacy-interface-list"},
		dataplane.ResourceRoute: {"legacy-route-table"},
		dataplane.ResourceDNS:   {"legacy-dns"},
	}
	resources := &scriptedResources{
		snapshots: []platform.ResourceSnapshot{{
			dataplane.ResourceTUN:   {"if-lo0", "if-en0"},
			dataplane.ResourceRoute: {"if-en0:route-default"},
			dataplane.ResourceDNS:   {"legacy-dns"},
		}},
		legacySnapshots: []platform.ResourceSnapshot{legacyBaseline},
	}
	manager.Resources = resources
	if err := manager.Store.Save(&State{
		Schema:         SchemaV2,
		Phase:          PhaseActive,
		LeaseID:        "0123456789abcdef01234567",
		Mode:           dataplane.ModeTUN,
		Instance:       dataplane.Instance{Backend: "sing-box", Mode: dataplane.ModeTUN},
		ResourceBefore: legacyBaseline,
	}); err != nil {
		t.Fatal(err)
	}

	if stopped, err := manager.Stop(t.Context()); err != nil || !stopped {
		t.Fatalf(
			"stop = %v, %v; legacy snapshots = %d, want legacy snapshot compatibility",
			stopped, err, resources.legacyNext,
		)
	}
	if resources.legacyNext != 1 {
		t.Fatalf("legacy snapshots = %d, want 1", resources.legacyNext)
	}
}

func TestManagerStopRejectsRemainingRouteInLegacySnapshot(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	manager.Resources = &scriptedResources{
		legacySnapshots: []platform.ResourceSnapshot{{
			dataplane.ResourceRoute: {"legacy-route-table-with-fleet-route"},
		}},
	}
	if err := manager.Store.Save(&State{
		Schema:   SchemaV2,
		Phase:    PhaseActive,
		LeaseID:  "0123456789abcdef01234567",
		Mode:     dataplane.ModeTUN,
		Instance: dataplane.Instance{Backend: "sing-box", Mode: dataplane.ModeTUN},
		ResourceBefore: platform.ResourceSnapshot{
			dataplane.ResourceRoute: {"legacy-route-table"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.Stop(t.Context()); !dataplane.IsCode(err, dataplane.CodeStop) {
		t.Fatalf("stop error = %v, want remaining legacy route failure", err)
	}
}

func TestManagerStopRejectsRemainingOwnedRoute(t *testing.T) {
	plane := &fakePlane{id: "sing-box"}
	manager := newTestManager(t, "", plane)
	manager.Resources = &scriptedResources{snapshots: []platform.ResourceSnapshot{
		{
			dataplane.ResourceTUN:   {"if-lo0", "if-en0"},
			dataplane.ResourceRoute: {"if-en0:route-default"},
			dataplane.ResourceDNS:   {"dns-baseline"},
		},
		{
			dataplane.ResourceTUN:   {"if-lo0", "if-en0", "if-utun9"},
			dataplane.ResourceRoute: {"if-en0:route-default", "if-utun9:route-fleet"},
			dataplane.ResourceDNS:   {"dns-baseline"},
		},
		{
			dataplane.ResourceTUN:   {"if-lo0", "if-en0"},
			dataplane.ResourceRoute: {"if-en0:route-default", "if-utun9:route-fleet"},
			dataplane.ResourceDNS:   {"dns-baseline"},
		},
	}}

	if _, err := manager.Start(t.Context(), StartInput{
		Node: model.Node{Name: "node", Type: "vmess"},
		Mode: dataplane.ModeTUN, Port: 7890,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Stop(t.Context()); !dataplane.IsCode(err, dataplane.CodeStop) {
		t.Fatalf("stop error = %v, want remaining owned route failure", err)
	}
}
