package backend

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
