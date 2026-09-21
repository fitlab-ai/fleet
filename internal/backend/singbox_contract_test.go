package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
)

type contractRunner struct {
	mu         sync.Mutex
	calls      []contractRunnerCall
	curlResult platform.Result
	curlErr    error
}

type contractRunnerCall struct {
	command []string
	timeout time.Duration
}

func (r *contractRunner) Run(command []string, _ string, _ map[string]string, timeout time.Duration) (platform.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, contractRunnerCall{command: append([]string(nil), command...), timeout: timeout})
	r.mu.Unlock()
	if len(command) > 1 {
		switch command[1] {
		case "version":
			return platform.Result{Stdout: "sing-box version 1.13.14"}, nil
		case "check":
			return platform.Result{}, nil
		case "--proxy":
			return r.curlResult, r.curlErr
		}
	}
	return platform.Result{}, nil
}

func (r *contractRunner) count(argument string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if len(call.command) > 1 && call.command[1] == argument {
			count++
		}
	}
	return count
}

func (r *contractRunner) call(argument string) (contractRunnerCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if len(call.command) > 1 && call.command[1] == argument {
			return call, true
		}
	}
	return contractRunnerCall{}, false
}

func contractNodes() []model.Node {
	return []model.Node{
		{Name: "vmess", Type: "vmess", Server: "vmess.example", Port: 443, UUID: "00000000-0000-0000-0000-000000000001"},
		{
			Name: "hysteria2", Type: "hysteria2", Server: "hysteria.example", Port: 443,
			Password: "hysteria-password", SNI: "hysteria-sni.example", ALPN: []string{"h3"}, Up: 10, Down: 20,
			Extra: map[string]any{"_fleet_hysteria2": map[string]any{
				"server_ports": []string{"443:443", "1000:1002"}, "hop_interval": "5s",
				"obfs": map[string]any{"type": "salamander", "password": "obfs-password"},
			}},
		},
		{Name: "anytls", Type: "anytls", Server: "anytls.example", Port: 443, Password: "anytls-password", SNI: "anytls-sni.example"},
		{Name: "trojan", Type: "trojan", Server: "trojan.example", Port: 443, Password: "trojan-password", SNI: "trojan-sni.example"},
	}
}

func TestSingBoxDataPlaneContract(t *testing.T) {
	var _ dataplane.DataPlane = (*SingBox)(nil)
	box := &SingBox{}
	if box.ID() != "sing-box" {
		t.Fatalf("ID() = %q", box.ID())
	}
	capabilities, err := box.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(capabilities.Modes, []dataplane.Mode{dataplane.ModeProxy, dataplane.ModeTUN}) ||
		!slices.Equal(capabilities.Protocols, []string{"vmess", "hysteria2", "anytls", "trojan"}) ||
		capabilities.Version != ">=1.13.14" {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	for mode, want := range map[dataplane.Mode][]dataplane.ResourceKind{
		dataplane.ModeProxy: {dataplane.ResourceRuntimeLock, dataplane.ResourceProcess, dataplane.ResourcePort, dataplane.ResourceSystemProxy},
		dataplane.ModeTUN:   {dataplane.ResourceRuntimeLock, dataplane.ResourceProcess, dataplane.ResourcePort, dataplane.ResourceTUN, dataplane.ResourceRoute, dataplane.ResourceDNS},
	} {
		if !slices.Equal(capabilities.Resources[mode], want) {
			t.Fatalf("resources[%s] = %v, want %v", mode, capabilities.Resources[mode], want)
		}
	}
	for name, request := range map[string]dataplane.ValidateRequest{
		"mode":     {Backend: box.ID(), Purpose: dataplane.ValidationStart, Mode: "future", Nodes: []model.Node{contractNodes()[0]}},
		"protocol": {Backend: box.ID(), Purpose: dataplane.ValidationStart, Mode: dataplane.ModeProxy, Nodes: []model.Node{{Type: "future"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := capabilities.Require(request); !dataplane.IsCode(err, dataplane.CodeUnsupported) {
				t.Fatalf("Require() error = %v, want unsupported", err)
			}
		})
	}
}

func TestSingBoxRenderContractMatrix(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := &contractRunner{}
	box := &SingBox{
		Binary: binary, Runner: runner,
		Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}},
	}
	const port = 17890
	for _, node := range contractNodes() {
		for _, mode := range []dataplane.Mode{dataplane.ModeProxy, dataplane.ModeTUN} {
			t.Run(node.Type+"/"+string(mode), func(t *testing.T) {
				endpoint := dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: port}
				validate := dataplane.ValidateRequest{
					Backend: box.ID(), Purpose: dataplane.ValidationStart, Mode: mode,
					Nodes: []model.Node{node}, Endpoint: endpoint,
				}
				capabilities, capErr := box.Capabilities(t.Context())
				if capErr != nil {
					t.Fatal(capErr)
				}
				if err := capabilities.Require(validate); err != nil {
					t.Fatal(err)
				}
				if err := box.Validate(t.Context(), validate); err != nil {
					t.Fatal(err)
				}
				artifact, err := box.Render(t.Context(), dataplane.RenderRequest{
					Backend: box.ID(), Purpose: dataplane.ValidationStart, Mode: mode,
					Node: node, Endpoint: endpoint,
				})
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(artifact.Bytes)
				if artifact.Backend != box.ID() || artifact.Format != "json" || artifact.Filename != "sing-box.json" ||
					artifact.SHA256 != hex.EncodeToString(sum[:]) || len(artifact.Bytes) == 0 {
					t.Fatalf("artifact = %#v", artifact)
				}
				path := filepath.Join(t.TempDir(), artifact.Filename)
				if err := os.WriteFile(path, artifact.Bytes, 0o600); err != nil {
					t.Fatal(err)
				}
				stored, err := os.ReadFile(path)
				if err != nil || !slices.Equal(stored, artifact.Bytes) {
					t.Fatalf("stored artifact mismatch: %v", err)
				}
				var config map[string]any
				if err := json.Unmarshal(artifact.Bytes, &config); err != nil {
					t.Fatal(err)
				}
				inbounds := config["inbounds"].([]any)
				wantTUN := mode == dataplane.ModeTUN
				foundMixed, foundTUN := false, false
				for _, raw := range inbounds {
					inbound := raw.(map[string]any)
					switch inbound["type"] {
					case "mixed":
						foundMixed = inbound["listen"] == endpoint.Host && int(inbound["listen_port"].(float64)) == endpoint.Port
					case "tun":
						foundTUN = true
					}
				}
				if !foundMixed || foundTUN != wantTUN {
					t.Fatalf("inbounds = %#v", inbounds)
				}
				outbounds := config["outbounds"].([]any)
				proxy := outbounds[0].(map[string]any)
				direct := outbounds[1].(map[string]any)
				if proxy["type"] != node.Type || proxy["tag"] != "proxy" || direct["type"] != "direct" || direct["tag"] != "direct" {
					t.Fatalf("outbounds = %#v", outbounds)
				}
				if wantTUN {
					if config["dns"] == nil || config["route"] == nil {
						t.Fatalf("TUN config misses DNS or route: %#v", config)
					}
					tun := inbounds[0].(map[string]any)
					if len(tun["route_exclude_address"].([]any)) != 1 {
						t.Fatalf("TUN route exclusions = %#v", tun["route_exclude_address"])
					}
				}
			})
		}
	}
	if runner.count("version") != 16 || runner.count("check") != 8 {
		t.Fatalf("runner calls: version=%d check=%d, want 16 and 8", runner.count("version"), runner.count("check"))
	}
}

func TestSingBoxProbeInstanceContract(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := dataplane.ListenEndpoint{Network: "tcp", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port}
	box := &SingBox{}
	instance := &dataplane.Instance{Process: dataplane.ProcessIdentity{PID: 1}}
	result, err := box.Probe(t.Context(), dataplane.ProbeRequest{Kind: dataplane.ProbeInstance, Instance: instance, Endpoint: endpoint})
	if err != nil || result.Status != dataplane.HealthHealthy || len(result.Resources) != 1 {
		t.Fatalf("healthy probe = %#v, %v", result, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = box.Probe(t.Context(), dataplane.ProbeRequest{Kind: dataplane.ProbeInstance, Instance: instance, Endpoint: endpoint})
	if err != nil || result.Status != dataplane.HealthStarting || result.Reason != dataplane.CodeProbe {
		t.Fatalf("closed probe = %#v, %v", result, err)
	}
}

type probeProcessHarness struct {
	mu            sync.Mutex
	pid           int
	listen        bool
	immediateExit bool
	alive         bool
	identity      dataplane.ProcessIdentity
	listener      net.Listener
	done          chan struct{}
	stopOnce      sync.Once
	runtimeDir    string
	signals       []os.Signal
}

func newProbeProcessHarness(listen, immediateExit bool) *probeProcessHarness {
	return &probeProcessHarness{pid: 999999999, listen: listen, immediateExit: immediateExit, done: make(chan struct{})}
}

func (h *probeProcessHarness) Start(_ context.Context, request platform.LaunchRequest) (platform.ProcessHandle, error) {
	var configPath string
	for i, argument := range request.Command {
		if argument == "-c" && i+1 < len(request.Command) {
			configPath = request.Command[i+1]
		}
		if argument == "-D" && i+1 < len(request.Command) {
			h.runtimeDir = request.Command[i+1]
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	port := 0
	for _, raw := range config["inbounds"].([]any) {
		inbound := raw.(map[string]any)
		if inbound["type"] == "mixed" {
			port = int(inbound["listen_port"].(float64))
		}
	}
	h.mu.Lock()
	h.alive = true
	h.identity = dataplane.ProcessIdentity{
		PID: h.pid, Executable: request.Command[0],
		ArgsFingerprint: platform.FingerprintArgs(request.Command),
	}
	h.mu.Unlock()
	if h.listen {
		h.listener, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", stringPort(port)))
		if err != nil {
			return nil, err
		}
	}
	if request.Stdout != nil {
		_, _ = io.WriteString(request.Stdout, "probe process started\n")
	}
	if h.immediateExit {
		h.stop()
	}
	return probeProcessHandle{harness: h}, nil
}

func stringPort(port int) string {
	return strconv.Itoa(port)
}

func (h *probeProcessHarness) Inspect(pid int) (dataplane.ProcessIdentity, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if pid != h.pid || !h.alive {
		return dataplane.ProcessIdentity{}, os.ErrNotExist
	}
	return h.identity, nil
}

func (h *probeProcessHarness) Signal(_ dataplane.ProcessIdentity, signal os.Signal) error {
	h.mu.Lock()
	h.signals = append(h.signals, signal)
	h.mu.Unlock()
	h.stop()
	return nil
}

func (h *probeProcessHarness) stop() {
	h.stopOnce.Do(func() {
		h.mu.Lock()
		h.alive = false
		listener := h.listener
		h.mu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}
		close(h.done)
	})
}

func (h *probeProcessHarness) snapshot() (string, bool, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runtimeDir, h.alive, len(h.signals)
}

func (h *probeProcessHarness) port() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listener == nil {
		return 0
	}
	return h.listener.Addr().(*net.TCPAddr).Port
}

type probeProcessHandle struct{ harness *probeProcessHarness }

func (h probeProcessHandle) PID() int         { return h.harness.pid }
func (h probeProcessHandle) Wait() error      { <-h.harness.done; return nil }
func (h probeProcessHandle) Terminate() error { h.harness.stop(); return nil }
func (h probeProcessHandle) Kill() error      { h.harness.stop(); return nil }

func TestSingBoxProbeOutboundContract(t *testing.T) {
	for _, test := range []struct {
		name          string
		listen        bool
		immediateExit bool
		curl          platform.Result
		curlErr       error
		wantStatus    dataplane.HealthStatus
		wantReason    dataplane.ErrorCode
		cancelAfter   time.Duration
		wantErr       error
		wantSignals   int
		wantCurlCalls int
		maxDuration   time.Duration
	}{
		{name: "healthy HTTPS", listen: true, curl: platform.Result{Stdout: "204"}, wantStatus: dataplane.HealthHealthy, wantSignals: 1, wantCurlCalls: 1},
		{name: "TCP ready but HTTPS failed", listen: true, curl: platform.Result{Stdout: "000"}, wantStatus: dataplane.HealthUnhealthy, wantReason: dataplane.CodeProbe, wantSignals: 1, wantCurlCalls: 1},
		{name: "HTTPS runner failed", listen: true, curlErr: errors.New("curl failed"), wantStatus: dataplane.HealthUnhealthy, wantReason: dataplane.CodeProbe, wantSignals: 1, wantCurlCalls: 1},
		{name: "HTTPS runner timed out", listen: true, curlErr: context.DeadlineExceeded, wantStatus: dataplane.HealthUnhealthy, wantReason: dataplane.CodeProbe, wantSignals: 1, wantCurlCalls: 1},
		{name: "core exits before readiness", immediateExit: true, wantStatus: dataplane.HealthUnhealthy, wantReason: dataplane.CodeStart, maxDuration: 500 * time.Millisecond},
		{name: "readiness canceled", cancelAfter: 80 * time.Millisecond, wantErr: context.DeadlineExceeded, wantSignals: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newProbeProcessHarness(test.listen, test.immediateExit)
			runner := &contractRunner{curlResult: test.curl, curlErr: test.curlErr}
			box := &SingBox{
				Binary: "/usr/local/bin/sing-box", Runner: runner,
				Launcher: harness, Inspector: harness,
			}
			ctx := t.Context()
			if test.cancelAfter > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.cancelAfter)
				defer cancel()
			}
			node := contractNodes()[0]
			started := time.Now()
			result, err := box.Probe(ctx, dataplane.ProbeRequest{
				Kind: dataplane.ProbeOutbound, Node: &node, Target: "https://health.example/status",
			})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("Probe() error = %v, want %v", err, test.wantErr)
				}
			} else if err != nil || result.Status != test.wantStatus || result.Reason != test.wantReason {
				t.Fatalf("Probe() = %#v, %v", result, err)
			}
			if test.maxDuration > 0 && time.Since(started) > test.maxDuration {
				t.Fatalf("Probe() took %s, want no more than %s after core exit", time.Since(started), test.maxDuration)
			}
			root, alive, signals := harness.snapshot()
			if alive || signals != test.wantSignals {
				t.Fatalf("process after probe: alive=%v signals=%d, want signals=%d", alive, signals, test.wantSignals)
			}
			if root == "" {
				t.Fatal("probe runtime directory was not captured")
			}
			if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("probe runtime directory remains: %s (%v)", root, statErr)
			}
			if got := runner.count("--proxy"); got != test.wantCurlCalls {
				t.Fatalf("curl calls = %d, want %d", got, test.wantCurlCalls)
			}
			if test.wantCurlCalls == 1 {
				call, ok := runner.call("--proxy")
				if !ok {
					t.Fatal("curl call was not recorded")
				}
				want := []string{
					"curl", "--proxy", "http://127.0.0.1:" + stringPort(harness.port()),
					"--noproxy", "", "--silent", "--output", "/dev/null",
					"--write-out", "%{http_code}", "--connect-timeout", "10",
					"--max-time", "10", "https://health.example/status",
				}
				if !slices.Equal(call.command, want) || call.timeout != 12*time.Second {
					t.Fatalf("curl call = %q timeout=%s, want %q timeout=%s", call.command, call.timeout, want, 12*time.Second)
				}
			}
		})
	}
}

var _ platform.Launcher = (*probeProcessHarness)(nil)
var _ platform.ProcessInspector = (*probeProcessHarness)(nil)
var _ platform.ProcessHandle = probeProcessHandle{}
