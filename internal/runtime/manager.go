package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
	"github.com/fitlab-ai/fleet/internal/store"
)

const defaultCleanupTimeout = 10 * time.Second

type StartInput struct {
	Node      model.Node
	NodeRef   NodeRef
	Mode      dataplane.Mode
	Port      int
	Extension *dataplane.Extension
}

type Manager struct {
	Registry        *dataplane.Registry
	Store           *StateStore
	RuntimeDir      string
	Configured      dataplane.BackendID
	Proxy           platform.ProxyManager
	Resources       platform.ResourceInspector
	PortBinder      platform.PortBinder
	CleanupTimeout  time.Duration
	ReconcileLegacy func(*State) error
}

type runtimeLockContextKey struct{}

func (m *Manager) Start(ctx context.Context, input StartInput) (*State, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	release, err := m.acquireRuntimeLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	if _, err := m.Store.Load(); err == nil {
		return nil, dataplane.NewError(
			dataplane.CodeResourceConflict, "start", m.Configured,
			"Fleet already has an active runtime", nil,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	plane, err := m.Registry.Configured(m.Configured)
	if err != nil {
		return nil, err
	}
	endpoint := dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: input.Port}
	request := dataplane.ValidateRequest{
		Backend: plane.ID(), Purpose: dataplane.ValidationStart,
		Mode: input.Mode, Nodes: []model.Node{input.Node},
		Endpoint: endpoint, Extension: input.Extension,
	}
	capabilities, err := plane.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	if err := capabilities.Require(request); err != nil {
		return nil, err
	}
	if err := plane.Validate(ctx, request); err != nil {
		return nil, err
	}
	if input.Mode == dataplane.ModeTUN {
		if err := m.checkFullTunnelConflicts(ctx, nil, false); err != nil {
			return nil, err
		}
	}
	if err := m.checkPort(ctx, endpoint); err != nil {
		return nil, err
	}
	artifact, err := plane.Render(ctx, dataplane.RenderRequest{
		Backend: plane.ID(), Purpose: dataplane.ValidationStart,
		Mode: input.Mode, Node: input.Node, Endpoint: endpoint,
		Extension: input.Extension,
	})
	if err != nil {
		return nil, err
	}
	resourceKinds := managedNetworkResources(capabilities.Resources[input.Mode])
	resourceBefore := platform.ResourceSnapshot(nil)
	if len(resourceKinds) > 0 && m.Resources != nil {
		resourceBefore, err = m.Resources.Snapshot(ctx, resourceKinds)
		if err != nil {
			return nil, dataplane.NewError(
				dataplane.CodeResourceConflict, "resource-snapshot", plane.ID(),
				"Runtime network resources could not be inspected", err,
			)
		}
	}

	leaseID, err := newLeaseID()
	if err != nil {
		return nil, fmt.Errorf("create runtime lease: %w", err)
	}
	instanceDir := filepath.Join(m.RuntimeDir, "runtime", leaseID)
	filename := filepath.Base(artifact.Filename)
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		filename = "config.json"
	}
	artifactPath := filepath.Join(instanceDir, filename)
	if err := store.AtomicWrite(artifactPath, artifact.Bytes); err != nil {
		return nil, fmt.Errorf("write data plane config: %w", err)
	}

	proxyBefore := platform.ProxySnapshot(nil)
	if input.Mode == dataplane.ModeProxy {
		proxyBefore, err = m.proxy().Snapshot(ctx)
		if err != nil {
			return nil, errors.Join(err, os.RemoveAll(instanceDir))
		}
	}
	state := &State{
		Schema: SchemaV2, Phase: PhasePreparing, LeaseID: leaseID,
		Node: input.NodeRef, Mode: input.Mode, Port: input.Port,
		Instance: dataplane.Instance{
			ID: leaseID, Backend: plane.ID(), Mode: input.Mode,
		},
		Claims:                  buildClaims(capabilities.Resources[input.Mode], leaseID, endpoint),
		ResourceBefore:          resourceBefore,
		ResourceSnapshotVersion: ResourceSnapshotOwnershipV1,
		SystemProxyBefore:       proxyBefore,
	}
	if state.Node.Name == "" {
		state.Node.Name = input.Node.Name
	}
	if err := m.Store.Save(state); err != nil {
		return nil, errors.Join(err, os.RemoveAll(instanceDir))
	}

	instance, err := plane.Start(ctx, dataplane.StartRequest{
		ArtifactPath: artifactPath, RuntimeDir: instanceDir, Mode: input.Mode,
		LeaseID: leaseID, Endpoint: endpoint,
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, m.failStart(ctx, state, plane, instance, err)
	}
	state.Instance = instance
	state.Instance.ID = leaseID
	state.Instance.Backend = plane.ID()
	state.Instance.Mode = input.Mode
	if err := m.Store.Save(state); err != nil {
		return nil, m.failStart(ctx, state, plane, instance, err)
	}

	health, err := probeUntilHealthy(ctx, plane, &state.Instance, endpoint)
	if err != nil {
		return nil, m.failStart(ctx, state, plane, instance, err)
	}
	if health.Status != dataplane.HealthHealthy {
		err = dataplane.NewError(
			dataplane.CodeProbe, "start-probe", plane.ID(),
			"The data plane did not become healthy", nil,
		)
		return nil, m.failStart(ctx, state, plane, instance, err)
	}
	if err := m.verifyResourcesChanged(ctx, state, resourceKinds); err != nil {
		return nil, m.failStart(ctx, state, plane, instance, err)
	}
	if input.Mode == dataplane.ModeTUN {
		if err := m.checkFullTunnelConflicts(
			ctx, state.ResourceOwned[dataplane.ResourceTUN], true,
		); err != nil {
			return nil, m.failStart(ctx, state, plane, instance, err)
		}
	}
	if input.Mode == dataplane.ModeProxy {
		if err := m.proxy().Enable(ctx, endpoint.Host, endpoint.Port); err != nil {
			return nil, m.failStart(ctx, state, plane, instance, err)
		}
	}
	state.Phase = PhaseActive
	for i := range state.Claims {
		state.Claims[i].Status = ClaimActive
	}
	if err := m.Store.Save(state); err != nil {
		return nil, m.failStart(ctx, state, plane, instance, err)
	}
	return state, nil
}

func probeUntilHealthy(
	ctx context.Context,
	plane dataplane.DataPlane,
	instance *dataplane.Instance,
	endpoint dataplane.ListenEndpoint,
) (dataplane.HealthResult, error) {
	var health dataplane.HealthResult
	for attempt := 0; attempt < 20; attempt++ {
		var err error
		health, err = plane.Probe(ctx, dataplane.ProbeRequest{
			Kind: dataplane.ProbeInstance, Instance: instance, Endpoint: endpoint,
		})
		if err != nil || health.Status == dataplane.HealthHealthy {
			return health, err
		}
		if health.Status != dataplane.HealthStarting && health.Status != dataplane.HealthUnknown {
			return health, nil
		}
		select {
		case <-ctx.Done():
			return health, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return health, nil
}

func (m *Manager) Stop(ctx context.Context) (bool, error) {
	if err := m.ready(); err != nil {
		return false, err
	}
	release, err := m.acquireRuntimeLock(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	state, err := m.Store.Load()
	if errors.Is(err, os.ErrNotExist) {
		// Without a state file there is no verifiable ownership of any running
		// process, so Fleet deliberately refuses to signal arbitrary sing-box
		// processes. This window is intentionally narrow: the state file is
		// written before the data plane starts, and a partially launched
		// process is torn down by the backend before Start reports failure.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state.ReadOnly {
		return false, dataplane.NewError(
			dataplane.CodeStateCorrupt, "stop", state.Instance.Backend,
			"Fleet runtime state was created by a newer version", nil,
		)
	}
	if err := m.reconcileLegacy(state); err != nil {
		return false, err
	}
	plane, err := m.Registry.Configured(state.Instance.Backend)
	if err != nil {
		return false, err
	}
	state.Phase = PhaseReleasing
	if err := m.Store.Save(state); err != nil {
		return false, err
	}
	if err := plane.Stop(ctx, state.Instance); err != nil {
		state.Phase, state.LastError = PhaseDegraded, err.Error()
		_ = m.Store.Save(state)
		return false, err
	}
	if err := m.verifyResourcesRestored(ctx, state); err != nil {
		state.Phase, state.LastError = PhaseDegraded, err.Error()
		_ = m.Store.Save(state)
		return false, err
	}
	if state.Mode == dataplane.ModeProxy {
		owned, ownErr := m.proxy().OwnedBy(ctx, "127.0.0.1", state.Port)
		if ownErr != nil {
			return false, ownErr
		}
		if owned {
			if err := m.proxy().Restore(ctx, state.SystemProxyBefore, "127.0.0.1", state.Port); err != nil {
				state.Phase, state.LastError = PhaseDegraded, err.Error()
				_ = m.Store.Save(state)
				return false, err
			}
		}
	}
	if err := m.removeInstanceDir(state); err != nil {
		return false, err
	}
	if err := m.Store.Remove(); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) Status(ctx context.Context) (*State, dataplane.HealthResult, error) {
	if err := m.ready(); err != nil {
		return nil, dataplane.HealthResult{}, err
	}
	state, err := m.Store.Load()
	if err != nil {
		return nil, dataplane.HealthResult{}, err
	}
	if err := m.reconcileLegacy(state); err != nil {
		return state, dataplane.HealthResult{Status: dataplane.HealthUnknown}, err
	}
	plane, err := m.Registry.Configured(state.Instance.Backend)
	if err != nil {
		return state, dataplane.HealthResult{}, err
	}
	health, err := plane.Probe(ctx, dataplane.ProbeRequest{
		Kind: dataplane.ProbeInstance, Instance: &state.Instance,
		Endpoint: dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: state.Port},
	})
	return state, health, err
}

func (m *Manager) Switch(ctx context.Context, input StartInput) (*State, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	release, err := m.acquireRuntimeLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	lockedCtx := context.WithValue(ctx, runtimeLockContextKey{}, m)
	if _, err := m.Stop(lockedCtx); err != nil {
		return nil, err
	}
	return m.Start(lockedCtx, input)
}

func (m *Manager) ready() error {
	if m.Registry == nil || m.Store == nil || m.RuntimeDir == "" {
		return dataplane.NewError(
			dataplane.CodeInvalidRequest, "runtime", m.Configured,
			"Runtime manager is not configured", nil,
		)
	}
	return nil
}

func (m *Manager) reconcileLegacy(state *State) error {
	if !state.Legacy {
		return nil
	}
	if m.ReconcileLegacy == nil {
		return dataplane.NewError(
			dataplane.CodeOwnershipUnknown, "legacy-reconcile", state.Instance.Backend,
			"Legacy process ownership could not be verified", nil,
		)
	}
	if err := m.ReconcileLegacy(state); err != nil {
		return err
	}
	return m.Store.Save(state)
}

func (m *Manager) acquireRuntimeLock(ctx context.Context) (func(), error) {
	if ctx.Value(runtimeLockContextKey{}) == m {
		return func() {}, nil
	}
	lock := &store.PIDLock{Path: filepath.Join(m.RuntimeDir, "runtime.lock")}
	if err := lock.Acquire(); err != nil {
		return nil, err
	}
	return lock.Release, nil
}

func (m *Manager) proxy() platform.ProxyManager {
	if m.Proxy == nil {
		return platform.NoopProxy{}
	}
	return m.Proxy
}

func (m *Manager) checkPort(ctx context.Context, endpoint dataplane.ListenEndpoint) error {
	binder := m.PortBinder
	if binder == nil {
		binder = platform.NetPortBinder{}
	}
	address := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
	tcp, err := binder.Listen(ctx, "tcp", address)
	if err != nil {
		return m.portConflict(ctx, endpoint, "tcp", err)
	}
	udp, err := binder.ListenPacket(ctx, "udp", address)
	if err != nil {
		closeErr := tcp.Close()
		return m.portConflict(ctx, endpoint, "udp", errors.Join(err, closeErr))
	}
	closeErr := errors.Join(udp.Close(), tcp.Close())
	if closeErr != nil {
		return m.portConflict(ctx, endpoint, "tcp/udp", closeErr)
	}
	return nil
}

func (m *Manager) portConflict(
	ctx context.Context,
	endpoint dataplane.ListenEndpoint,
	network string,
	cause error,
) error {
	message := "The local proxy " + network + " port is unavailable"
	if m.Resources != nil {
		ownerEndpoint := endpoint
		ownerEndpoint.Network = network
		owner, err := m.Resources.PortOwner(ctx, ownerEndpoint)
		if err == nil && owner.PID > 0 {
			message += " because it is used by process " + strconv.Itoa(owner.PID)
		}
	}
	return dataplane.NewError(
		dataplane.CodeResourceConflict, "port-preflight", m.Configured,
		message, cause,
	)
}

func (m *Manager) checkFullTunnelConflicts(
	ctx context.Context,
	allowedInterfaces []string,
	requireOwned bool,
) error {
	if m.Resources == nil {
		return dataplane.NewError(
			dataplane.CodeResourceConflict, "full-tunnel-preflight", m.Configured,
			"Full-tunnel routes could not be inspected", nil,
		)
	}
	observations, err := m.Resources.FullTunnels(ctx)
	if err != nil {
		code := dataplane.CodeResourceConflict
		operation := "full-tunnel-preflight"
		message := "Full-tunnel routes could not be inspected"
		if requireOwned {
			code = dataplane.CodeProbe
			operation = "full-tunnel-probe"
			message = "Full-tunnel routes could not be verified before activation"
		}
		return dataplane.NewError(code, operation, m.Configured, message, err)
	}
	if !requireOwned {
		if len(observations) == 0 {
			return nil
		}
		return dataplane.NewError(
			dataplane.CodeResourceConflict, "full-tunnel-preflight", m.Configured,
			"Another full-tunnel interface is already active", nil,
		)
	}
	if len(allowedInterfaces) != 1 || len(observations) == 0 {
		return dataplane.NewError(
			dataplane.CodeProbe, "full-tunnel-probe", m.Configured,
			"The data plane did not establish one owned full-tunnel interface", nil,
		)
	}
	allowed := allowedInterfaces[0]
	for _, observation := range observations {
		fingerprints := platform.FingerprintInterfaceNames(observation.Interface)
		if len(fingerprints) != 1 || fingerprints[0] != allowed {
			return dataplane.NewError(
				dataplane.CodeProbe, "full-tunnel-probe", m.Configured,
				"Another full-tunnel interface appeared during startup", nil,
			)
		}
	}
	return nil
}

func (m *Manager) failStart(
	parent context.Context,
	state *State,
	plane dataplane.DataPlane,
	instance dataplane.Instance,
	startErr error,
) error {
	timeout := m.CleanupTimeout
	if timeout <= 0 {
		timeout = defaultCleanupTimeout
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
	defer cancel()
	var cleanupErr error
	if instance.Process.PID > 0 {
		cleanupErr = plane.Stop(cleanupCtx, instance)
	}
	if cleanupErr == nil {
		cleanupErr = m.verifyResourcesRestored(cleanupCtx, state)
	}
	if state.Mode == dataplane.ModeProxy {
		owned, err := m.proxy().OwnedBy(cleanupCtx, "127.0.0.1", state.Port)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		} else if owned {
			cleanupErr = errors.Join(cleanupErr, m.proxy().Restore(cleanupCtx, state.SystemProxyBefore, "127.0.0.1", state.Port))
		}
	}
	if cleanupErr == nil {
		cleanupErr = m.removeInstanceDir(state)
	}
	if cleanupErr == nil {
		cleanupErr = m.Store.Remove()
	}
	if cleanupErr != nil {
		state.Phase, state.LastError = PhaseDegraded, startErr.Error()
		_ = m.Store.Save(state)
		return errors.Join(startErr, cleanupErr)
	}
	return startErr
}

func buildClaims(
	kinds []dataplane.ResourceKind,
	leaseID string,
	endpoint dataplane.ListenEndpoint,
) []Claim {
	claims := make([]Claim, 0, len(kinds))
	for _, kind := range kinds {
		key := string(kind)
		if kind == dataplane.ResourcePort {
			key = endpoint.Host + ":" + strconv.Itoa(endpoint.Port)
		}
		claims = append(claims, Claim{
			Kind: kind, Key: key, Owner: leaseID,
			ManagedBy: "fleet", Status: ClaimReserved,
		})
	}
	return claims
}

func managedNetworkResources(kinds []dataplane.ResourceKind) []dataplane.ResourceKind {
	var managed []dataplane.ResourceKind
	for _, kind := range kinds {
		switch kind {
		case dataplane.ResourceTUN, dataplane.ResourceRoute, dataplane.ResourceDNS:
			managed = append(managed, kind)
		}
	}
	return managed
}

func (m *Manager) verifyResourcesChanged(
	ctx context.Context,
	state *State,
	kinds []dataplane.ResourceKind,
) error {
	if len(kinds) == 0 || m.Resources == nil {
		return nil
	}
	current, err := m.Resources.Snapshot(ctx, kinds)
	if err != nil {
		return dataplane.NewError(
			dataplane.CodeProbe, "resource-probe", state.Instance.Backend,
			"Runtime network resources could not be verified", err,
		)
	}
	owned := platform.ResourceSnapshot{}
	ownedTUN := snapshotDifference(
		current[dataplane.ResourceTUN],
		state.ResourceBefore[dataplane.ResourceTUN],
	)
	for _, kind := range kinds {
		if !snapshotContainsAll(current[kind], state.ResourceBefore[kind]) {
			return dataplane.NewError(
				dataplane.CodeProbe, "resource-probe", state.Instance.Backend,
				"Runtime network resources replaced a pre-existing resource", nil,
			)
		}
		// sing-box can capture DNS through the TUN route without rewriting the
		// host resolver. The baseline is still recorded so stop can prove that
		// Fleet did not leave a resolver mutation behind.
		if kind == dataplane.ResourceDNS {
			continue
		}
		added := snapshotDifference(current[kind], state.ResourceBefore[kind])
		if kind == dataplane.ResourceRoute {
			added = routesForInterfaces(added, ownedTUN)
		}
		if len(added) == 0 {
			return dataplane.NewError(
				dataplane.CodeProbe, "resource-probe", state.Instance.Backend,
				"The data plane did not establish all declared network resources", nil,
			)
		}
		owned[kind] = added
	}
	state.ResourceOwned = owned
	return nil
}

func (m *Manager) verifyResourcesRestored(ctx context.Context, state *State) error {
	kinds := make([]dataplane.ResourceKind, 0, len(state.ResourceBefore))
	for kind := range state.ResourceBefore {
		kinds = append(kinds, kind)
	}
	if len(kinds) == 0 || m.Resources == nil {
		return nil
	}
	var (
		current platform.ResourceSnapshot
		err     error
	)
	legacySnapshot := state.ResourceSnapshotVersion == ResourceSnapshotLegacy &&
		len(state.ResourceOwned) == 0
	if legacySnapshot {
		if legacy, ok := m.Resources.(platform.LegacyResourceSnapshotter); ok {
			current, err = legacy.SnapshotLegacy(ctx, kinds)
		} else {
			current, err = m.Resources.Snapshot(ctx, kinds)
		}
	} else {
		current, err = m.Resources.Snapshot(ctx, kinds)
	}
	if err != nil {
		return dataplane.NewError(
			dataplane.CodeStop, "resource-restore", state.Instance.Backend,
			"Runtime network resources could not be verified after stop", err,
		)
	}
	for _, kind := range kinds {
		if legacySnapshot {
			if !slices.Equal(current[kind], state.ResourceBefore[kind]) {
				return dataplane.NewError(
					dataplane.CodeStop, "resource-restore", state.Instance.Backend,
					"Runtime network resources were not fully restored", nil,
				)
			}
			continue
		}
		// Fleet can only prove ownership of the resources it added (ResourceOwned).
		// Pre-existing baseline entries may legitimately disappear during a session
		// because of external network drift (roaming to another network changes the
		// default route and the DNS resolvers), so stop must not fail just because a
		// baseline entry is no longer present. Requiring every owned resource to be
		// gone is the check that guarantees Fleet cleaned up after itself.
		if owned, ok := state.ResourceOwned[kind]; ok {
			if snapshotsIntersect(current[kind], owned) {
				return dataplane.NewError(
					dataplane.CodeStop, "resource-restore", state.Instance.Backend,
					"Runtime network resources were not fully restored", nil,
				)
			}
		}
	}
	return nil
}

func snapshotContainsAll(current, baseline []string) bool {
	present := make(map[string]struct{}, len(current))
	for _, entry := range current {
		present[entry] = struct{}{}
	}
	for _, entry := range baseline {
		if _, ok := present[entry]; !ok {
			return false
		}
	}
	return true
}

func snapshotDifference(current, baseline []string) []string {
	known := make(map[string]struct{}, len(baseline))
	for _, entry := range baseline {
		known[entry] = struct{}{}
	}
	var added []string
	for _, entry := range current {
		if _, ok := known[entry]; !ok {
			added = append(added, entry)
		}
	}
	return added
}

func routesForInterfaces(routes, interfaces []string) []string {
	var owned []string
	for _, route := range routes {
		for _, interfaceFingerprint := range interfaces {
			if len(route) > len(interfaceFingerprint) &&
				route[:len(interfaceFingerprint)] == interfaceFingerprint &&
				route[len(interfaceFingerprint)] == ':' {
				owned = append(owned, route)
				break
			}
		}
	}
	return owned
}

func snapshotsIntersect(current, owned []string) bool {
	present := make(map[string]struct{}, len(current))
	for _, entry := range current {
		present[entry] = struct{}{}
	}
	for _, entry := range owned {
		if _, ok := present[entry]; ok {
			return true
		}
	}
	return false
}

func newLeaseID() (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (m *Manager) removeInstanceDir(state *State) error {
	if state == nil || len(state.LeaseID) != 24 || filepath.Base(state.LeaseID) != state.LeaseID {
		return nil
	}
	if _, err := hex.DecodeString(state.LeaseID); err != nil {
		return nil
	}
	return os.RemoveAll(filepath.Join(m.RuntimeDir, "runtime", state.LeaseID))
}
