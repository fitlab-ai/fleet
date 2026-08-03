package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
)

type singBoxRunner struct {
	result   platform.Result
	err      error
	check    platform.Result
	checkErr error
}

func (r singBoxRunner) Run(command []string, _ string, _ map[string]string, _ time.Duration) (platform.Result, error) {
	if len(command) > 1 && command[1] == "check" {
		return r.check, r.checkErr
	}
	return r.result, r.err
}

func TestSingBoxVersionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		output string
		ok     bool
	}{
		{"sing-box version 1.13.14", true},
		{"sing-box version 1.14.0", true},
		{"sing-box version 1.13.13", false},
		{"sing-box version 1.14.0-beta.1", false},
		{"not a version", false},
	} {
		_, err := (SingBox{Runner: singBoxRunner{result: platform.Result{Stdout: tc.output}}}).CheckVersion()
		if (err == nil) != tc.ok {
			t.Errorf("output=%q err=%v, want ok=%v", tc.output, err, tc.ok)
		}
	}
}

func TestSingBoxValidationIdentifiesNodeSafely(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	node := model.Node{Name: "safe-name", Type: "vmess", Server: "example.com", Port: 443, UUID: "secret-uuid"}
	runner := singBoxRunner{result: platform.Result{Stdout: "sing-box version 1.13.14"}}
	box := SingBox{Binary: binary, Runner: runner}
	if err := box.ValidateNodes([]model.Node{node}, 7890); err != nil {
		t.Fatal(err)
	}
	box.Runner = singBoxRunner{
		result: platform.Result{Stdout: "sing-box version 1.13.14"},
		check:  platform.Result{Code: 1}, checkErr: errors.New("check failed"),
	}
	err = box.ValidateNodes([]model.Node{node}, 7890)
	if err == nil || !strings.Contains(err.Error(), "safe-name") || strings.Contains(err.Error(), "secret-uuid") {
		t.Fatalf("unsafe or missing node error: %v", err)
	}
}

type adapterHandle struct{ pid int }

func (h adapterHandle) PID() int  { return h.pid }
func (adapterHandle) Wait() error { return nil }
func (adapterHandle) Terminate() error {
	return nil
}
func (adapterHandle) Kill() error { return nil }

type adapterLauncher struct {
	request platform.LaunchRequest
}

func (l *adapterLauncher) Start(_ context.Context, request platform.LaunchRequest) (platform.ProcessHandle, error) {
	l.request = request
	return adapterHandle{pid: 4242}, nil
}

type adapterInspector struct {
	identity dataplane.ProcessIdentity
	signals  []os.Signal
}

func (i *adapterInspector) Inspect(int) (dataplane.ProcessIdentity, error) {
	return i.identity, nil
}
func (i *adapterInspector) Signal(_ dataplane.ProcessIdentity, signal os.Signal) error {
	i.signals = append(i.signals, signal)
	return nil
}

type acceptingAuthorizer struct{}

func (acceptingAuthorizer) Authorize(context.Context) error { return nil }

type recordingAuthorizer struct {
	calls int
	err   error
}

func (a *recordingAuthorizer) Authorize(context.Context) error {
	a.calls++
	return a.err
}

type recordingRunner struct {
	calls [][]string
}

func (r *recordingRunner) Run(command []string, _ string, _ map[string]string, _ time.Duration) (platform.Result, error) {
	r.calls = append(r.calls, append([]string(nil), command...))
	return platform.Result{}, nil
}

type exitingInspector struct {
	identity dataplane.ProcessIdentity
	checks   int
}

func (i *exitingInspector) Inspect(int) (dataplane.ProcessIdentity, error) {
	i.checks++
	if i.checks > 1 {
		return dataplane.ProcessIdentity{}, os.ErrNotExist
	}
	return i.identity, nil
}

func (*exitingInspector) Signal(dataplane.ProcessIdentity, os.Signal) error { return nil }

type fixedResolver struct {
	addresses []net.IPAddr
	err       error
}

func (r fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, r.err
}

type exitedHandle struct {
	pid int
	err error
}

func (h exitedHandle) PID() int       { return h.pid }
func (h exitedHandle) Wait() error    { return h.err }
func (exitedHandle) Terminate() error { return nil }
func (exitedHandle) Kill() error      { return nil }

type exitedLauncher struct {
	handle exitedHandle
}

func (l exitedLauncher) Start(
	context.Context,
	platform.LaunchRequest,
) (platform.ProcessHandle, error) {
	return l.handle, nil
}

type waitingDescendantInspector struct{}

func (waitingDescendantInspector) Inspect(int) (dataplane.ProcessIdentity, error) {
	return dataplane.ProcessIdentity{
		PID: 4242, Executable: "/usr/bin/sudo",
		ArgsFingerprint: platform.FingerprintArgs([]string{"sudo", "-n", "env"}),
	}, nil
}

func (waitingDescendantInspector) Signal(
	dataplane.ProcessIdentity,
	os.Signal,
) error {
	return nil
}

func (waitingDescendantInspector) ResolveDescendant(
	ctx context.Context,
	_ int,
	_ dataplane.ProcessIdentity,
) (dataplane.ProcessIdentity, error) {
	<-ctx.Done()
	return dataplane.ProcessIdentity{}, ctx.Err()
}

type cleanupHandle struct {
	mu             sync.Mutex
	terminated     bool
	killed         bool
	processStopped chan struct{}
	stopOnce       sync.Once
}

func newCleanupHandle() *cleanupHandle {
	return &cleanupHandle{processStopped: make(chan struct{})}
}

func (*cleanupHandle) PID() int { return 4242 }

func (h *cleanupHandle) Wait() error {
	<-h.processStopped
	return nil
}

func (h *cleanupHandle) Terminate() error {
	h.mu.Lock()
	h.terminated = true
	h.mu.Unlock()
	h.stopOnce.Do(func() { close(h.processStopped) })
	return nil
}

func (h *cleanupHandle) Kill() error {
	h.mu.Lock()
	h.killed = true
	h.mu.Unlock()
	h.stopOnce.Do(func() { close(h.processStopped) })
	return nil
}

func (h *cleanupHandle) cleanupSignals() (terminated, killed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.terminated, h.killed
}

type cleanupLauncher struct {
	handle  *cleanupHandle
	request *platform.LaunchRequest
}

func (l cleanupLauncher) Start(
	_ context.Context,
	request platform.LaunchRequest,
) (platform.ProcessHandle, error) {
	if l.request != nil {
		*l.request = request
	}
	return l.handle, nil
}

type descendantInspector struct {
	wrapper  dataplane.ProcessIdentity
	child    dataplane.ProcessIdentity
	rootPID  int
	expected dataplane.ProcessIdentity
	resolve  error
}

func (i *descendantInspector) Inspect(pid int) (dataplane.ProcessIdentity, error) {
	if pid == i.child.PID {
		return i.child, nil
	}
	return i.wrapper, nil
}

func (*descendantInspector) Signal(dataplane.ProcessIdentity, os.Signal) error { return nil }

func (i *descendantInspector) ResolveDescendant(
	_ context.Context,
	rootPID int,
	expected dataplane.ProcessIdentity,
) (dataplane.ProcessIdentity, error) {
	i.rootPID, i.expected = rootPID, expected
	if i.resolve != nil {
		return dataplane.ProcessIdentity{}, i.resolve
	}
	return i.child, nil
}

func TestSingBoxTUNStartTerminatesLauncherWhenChildIdentityCannotBeResolved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("privileged launch wrapper is only used by non-root Fleet")
	}
	handle := newCleanupHandle()
	inspector := &descendantInspector{
		wrapper: dataplane.ProcessIdentity{
			PID: 4242, Executable: "/usr/bin/sudo",
			ArgsFingerprint: platform.FingerprintArgs([]string{"sudo", "-n", "env"}),
		},
		resolve: errors.New("process identity unavailable"),
	}
	box := &SingBox{
		Binary:   "/opt/homebrew/bin/sing-box",
		Launcher: cleanupLauncher{handle: handle}, Inspector: inspector,
		Authorizer: acceptingAuthorizer{},
	}
	_, err := box.Start(t.Context(), dataplane.StartRequest{
		ArtifactPath: "/tmp/config.json", RuntimeDir: t.TempDir(),
		Mode: dataplane.ModeTUN, LeaseID: "lease",
		Endpoint: dataplane.ListenEndpoint{Host: "127.0.0.1", Port: 7891},
	})
	if err == nil {
		t.Fatal("Start() error = nil, want identity resolution failure")
	}
	terminated, killed := handle.cleanupSignals()
	if !terminated || killed {
		t.Fatalf("cleanup signals: terminated=%v killed=%v, want TERM without KILL", terminated, killed)
	}
}

func TestSingBoxTUNStartRecordsPrivilegedChildIdentity(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("privileged launch wrapper is only used by non-root Fleet")
	}
	args := []string{
		"/opt/homebrew/bin/sing-box", "run", "-c", "/tmp/config.json", "-D", t.TempDir(),
	}
	handle := newCleanupHandle()
	var launchRequest platform.LaunchRequest
	launcher := cleanupLauncher{handle: handle, request: &launchRequest}
	t.Cleanup(func() { _ = handle.Terminate() })
	inspector := &descendantInspector{
		wrapper: dataplane.ProcessIdentity{
			PID: 4242, Executable: "/usr/bin/sudo",
			ArgsFingerprint: platform.FingerprintArgs([]string{"sudo", "-n", "env"}),
		},
		child: dataplane.ProcessIdentity{
			PID: 5252, Executable: args[0],
			ArgsFingerprint: platform.FingerprintArgs(args),
		},
	}
	box := &SingBox{
		Binary: args[0], Launcher: launcher, Inspector: inspector,
		Authorizer: acceptingAuthorizer{},
	}
	instance, err := box.Start(t.Context(), dataplane.StartRequest{
		ArtifactPath: args[3], RuntimeDir: args[5], Mode: dataplane.ModeTUN,
		LeaseID: "lease", Endpoint: dataplane.ListenEndpoint{Host: "127.0.0.1", Port: 7891},
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.Process.PID != inspector.child.PID {
		t.Fatalf("process = %#v, want child %#v", instance.Process, inspector.child)
	}
	if inspector.rootPID != 4242 ||
		inspector.expected.ArgsFingerprint != inspector.child.ArgsFingerprint {
		t.Fatalf("resolver root=%d expected=%#v", inspector.rootPID, inspector.expected)
	}
	if launchRequest.NewSession {
		t.Fatal("TUN sudo launcher detached from the authorizing terminal session")
	}
	if !launchRequest.NewProcessGroup {
		t.Fatal("TUN sudo launcher did not isolate the data-plane process group")
	}
}

func TestSingBoxTUNStartReportsLauncherExitBeforeIdentityFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("privileged launch wrapper is only used by non-root Fleet")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	box := &SingBox{
		Binary: "/opt/homebrew/bin/sing-box",
		Launcher: exitedLauncher{handle: exitedHandle{
			pid: 4242, err: errors.New("exit status 1"),
		}},
		Inspector: waitingDescendantInspector{}, Authorizer: acceptingAuthorizer{},
	}
	_, err := box.Start(ctx, dataplane.StartRequest{
		ArtifactPath: "/tmp/config.json", RuntimeDir: t.TempDir(),
		Mode: dataplane.ModeTUN, LeaseID: "lease",
		Endpoint: dataplane.ListenEndpoint{Host: "127.0.0.1", Port: 7891},
	})
	var planeErr *dataplane.Error
	if !errors.As(err, &planeErr) || planeErr.Code != dataplane.CodeStart ||
		planeErr.PublicMessage != "Could not start sing-box" {
		t.Fatalf("error = %#v, want classified launcher exit", err)
	}
}

func TestSingBoxImplementsDataPlaneRenderAndStart(t *testing.T) {
	var _ dataplane.DataPlane = (*SingBox)(nil)
	launcher := &adapterLauncher{}
	inspector := &adapterInspector{}
	box := &SingBox{
		Binary: "/opt/homebrew/bin/sing-box", Launcher: launcher, Inspector: inspector,
	}
	node := model.Node{
		Name: "node", Type: "vmess", Server: "example.com", Port: 443, UUID: "uuid",
	}
	artifact, err := box.Render(t.Context(), dataplane.RenderRequest{
		Backend: box.ID(), Purpose: dataplane.ValidationStart,
		Mode: dataplane.ModeProxy, Node: node,
		Endpoint: dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: 7890},
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Backend != "sing-box" || artifact.Filename != "sing-box.json" ||
		!strings.Contains(string(artifact.Bytes), `"listen_port": 7890`) {
		t.Fatalf("artifact = %#v", artifact)
	}
	root := t.TempDir()
	configPath := filepath.Join(root, artifact.Filename)
	if err := os.WriteFile(configPath, artifact.Bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	instance, err := box.Start(t.Context(), dataplane.StartRequest{
		ArtifactPath: configPath, RuntimeDir: root, Mode: dataplane.ModeProxy,
		LeaseID: "lease", Endpoint: dataplane.ListenEndpoint{Host: "127.0.0.1", Port: 7890},
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.Process.PID != 4242 || instance.Backend != "sing-box" {
		t.Fatalf("instance = %#v", instance)
	}
	if got := strings.Join(launcher.request.Command, " "); !strings.Contains(got, "run -c "+configPath) {
		t.Fatalf("command = %q", got)
	}
	if launcher.request.Stdout == nil || launcher.request.Stderr == nil {
		t.Fatal("adapter did not capture process output")
	}
	_, _ = io.WriteString(launcher.request.Stdout, "redacted log")
}

func TestSingBoxTUNRenderPinsAndExcludesOneResolvedProxyServerAddress(t *testing.T) {
	box := &SingBox{Resolver: fixedResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("2001:db8::2")},
		{IP: net.ParseIP("103.181.165.120")},
		{IP: net.ParseIP("103.181.165.120")},
	}}}
	artifact, err := box.Render(t.Context(), dataplane.RenderRequest{
		Backend: box.ID(), Purpose: dataplane.ValidationStart,
		Mode: dataplane.ModeTUN,
		Node: model.Node{
			Name: "node", Type: "trojan", Server: "proxy.example.com",
			Port: 443, Password: "secret",
		},
		Endpoint: dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: 7891},
	})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(artifact.Bytes, &config); err != nil {
		t.Fatal(err)
	}
	inbound := config["inbounds"].([]any)[0].(map[string]any)
	got := inbound["route_exclude_address"]
	want := []any{"103.181.165.120/32"}
	if !slices.Equal(got.([]any), want) {
		t.Fatalf("route exclusions = %#v, want %#v", got, want)
	}
	outbound := config["outbounds"].([]any)[0].(map[string]any)
	if outbound["server"] != "103.181.165.120" {
		t.Fatalf("outbound server = %#v, want pinned IPv4 address", outbound["server"])
	}
	tls := outbound["tls"].(map[string]any)
	if tls["server_name"] != "proxy.example.com" {
		t.Fatalf("TLS server name = %#v, want logical hostname", tls["server_name"])
	}
}

func TestSingBoxTUNRenderPreservesImplicitWebSocketHostWhenDialingIP(t *testing.T) {
	box := &SingBox{Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}}}
	artifact, err := box.Render(t.Context(), dataplane.RenderRequest{
		Backend: box.ID(), Purpose: dataplane.ValidationStart,
		Mode: dataplane.ModeTUN,
		Node: model.Node{
			Name: "node", Type: "vmess", Server: "ws.example.com",
			Port: 443, UUID: "uuid", Network: "ws",
		},
		Endpoint: dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: 7891},
	})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(artifact.Bytes, &config); err != nil {
		t.Fatal(err)
	}
	outbound := config["outbounds"].([]any)[0].(map[string]any)
	if outbound["server"] != "203.0.113.7" {
		t.Fatalf("outbound server = %#v, want pinned address", outbound["server"])
	}
	transport := outbound["transport"].(map[string]any)
	headers := transport["headers"].(map[string]any)
	if headers["Host"] != "ws.example.com" {
		t.Fatalf("websocket Host = %#v, want logical hostname", headers["Host"])
	}
}

func TestSingBoxTUNRenderPreservesExplicitWebSocketHostWhenDialingIP(t *testing.T) {
	box := &SingBox{Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}}}
	artifact, err := box.Render(t.Context(), dataplane.RenderRequest{
		Backend: box.ID(), Purpose: dataplane.ValidationStart,
		Mode: dataplane.ModeTUN,
		Node: model.Node{
			Name: "node", Type: "vmess", Server: "ws.example.com",
			Port: 443, UUID: "uuid", Network: "ws",
			WSOpts: map[string]any{"headers": map[string]any{"host": "edge.example.com"}},
		},
		Endpoint: dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: 7891},
	})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(artifact.Bytes, &config); err != nil {
		t.Fatal(err)
	}
	outbound := config["outbounds"].([]any)[0].(map[string]any)
	transport := outbound["transport"].(map[string]any)
	headers := transport["headers"].(map[string]any)
	if headers["host"] != "edge.example.com" || headers["Host"] != nil {
		t.Fatalf("websocket headers = %#v, want explicit host unchanged", headers)
	}
}

func TestSingBoxResolveDialAddressUsesIPv6WhenIPv4IsUnavailable(t *testing.T) {
	box := &SingBox{Resolver: fixedResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("2001:db8::b")},
		{IP: net.ParseIP("2001:db8::a")},
	}}}
	dialServer, exclusion, err := box.resolveDialAddress(t.Context(), "proxy.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if dialServer != "2001:db8::a" || exclusion != "2001:db8::a/128" {
		t.Fatalf("dial target = %q, %q; want deterministic IPv6 target", dialServer, exclusion)
	}
}

func TestSingBoxResolveDialAddressRejectsNoUsableAddresses(t *testing.T) {
	box := &SingBox{Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.IP{}}}}}
	if _, _, err := box.resolveDialAddress(t.Context(), "proxy.example.com"); err == nil {
		t.Fatal("resolve succeeded without a usable address")
	}
}

func TestSingBoxTUNRenderRejectsUnresolvedProxyServer(t *testing.T) {
	box := &SingBox{Resolver: fixedResolver{err: errors.New("lookup failed")}}
	_, err := box.Render(t.Context(), dataplane.RenderRequest{
		Backend: box.ID(), Purpose: dataplane.ValidationStart,
		Mode: dataplane.ModeTUN,
		Node: model.Node{
			Name: "node", Type: "trojan", Server: "proxy.example.com",
			Port: 443, Password: "secret",
		},
		Endpoint: dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: 7891},
	})
	var planeErr *dataplane.Error
	if !errors.As(err, &planeErr) || planeErr.Code != dataplane.CodeResourceConflict {
		t.Fatalf("error = %#v, want resource-conflict", err)
	}
}

func TestSingBoxTUNExportRendersOfflineWithoutRouteExclusions(t *testing.T) {
	box := &SingBox{Resolver: fixedResolver{err: errors.New("lookup failed")}}
	node := model.Node{
		Name: "node", Type: "trojan", Server: "proxy.example.com",
		Port: 443, Password: "secret",
	}
	artifact, err := box.Render(t.Context(), dataplane.RenderRequest{
		Backend: box.ID(), Purpose: dataplane.ValidationExport,
		Mode: dataplane.ModeTUN, Node: node,
		Endpoint: dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: 7891},
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := BuildTUNConfig(node, 7891)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wantJSON = append(wantJSON, '\n')
	if string(artifact.Bytes) != string(wantJSON) {
		t.Fatalf("offline export:\n got %s\nwant %s", artifact.Bytes, wantJSON)
	}
}

func TestSingBoxTUNStopReauthorizesBeforePrivilegedSignal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("privileged stop authorization is only used by non-root Fleet")
	}
	identity := dataplane.ProcessIdentity{PID: 5252, Executable: "/opt/homebrew/bin/sing-box"}
	inspector := &exitingInspector{identity: identity}
	authorizer := &recordingAuthorizer{}
	runner := &recordingRunner{}
	box := &SingBox{Inspector: inspector, Authorizer: authorizer, Runner: runner}
	if err := box.Stop(t.Context(), dataplane.Instance{
		Mode: dataplane.ModeTUN, Process: identity,
	}); err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("authorization calls = %d, want 1", authorizer.calls)
	}
	if len(runner.calls) != 1 || strings.Join(runner.calls[0], " ") != "sudo -n kill -TERM 5252" {
		t.Fatalf("commands = %#v", runner.calls)
	}
}

func TestSingBoxTUNStopDeniedAuthorizationDoesNotSignal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("privileged stop authorization is only used by non-root Fleet")
	}
	identity := dataplane.ProcessIdentity{PID: 5252, Executable: "/opt/homebrew/bin/sing-box"}
	authorizer := &recordingAuthorizer{err: errors.New("denied")}
	runner := &recordingRunner{}
	box := &SingBox{
		Inspector:  &adapterInspector{identity: identity},
		Authorizer: authorizer, Runner: runner,
	}
	err := box.Stop(t.Context(), dataplane.Instance{
		Mode: dataplane.ModeTUN, Process: identity,
	})
	var planeErr *dataplane.Error
	if !errors.As(err, &planeErr) || planeErr.Code != dataplane.CodePermission {
		t.Fatalf("error = %#v, want permission", err)
	}
	if authorizer.calls != 1 || len(runner.calls) != 0 {
		t.Fatalf("authorization calls = %d, commands = %#v", authorizer.calls, runner.calls)
	}
}

func TestSingBoxCapabilitiesDeclareOwnedResources(t *testing.T) {
	capabilities, err := (&SingBox{}).Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := capabilities.Require(dataplane.ValidateRequest{
		Backend: "sing-box", Purpose: dataplane.ValidationStart,
		Mode: dataplane.ModeTUN, Nodes: []model.Node{{Type: "anytls"}},
	}); err != nil {
		t.Fatal(err)
	}
	resources := capabilities.Resources[dataplane.ModeTUN]
	for _, required := range []dataplane.ResourceKind{
		dataplane.ResourceProcess, dataplane.ResourcePort, dataplane.ResourceTUN,
		dataplane.ResourceRoute, dataplane.ResourceDNS,
	} {
		found := false
		for _, resource := range resources {
			found = found || resource == required
		}
		if !found {
			t.Errorf("TUN capability does not declare %s", required)
		}
	}
}
