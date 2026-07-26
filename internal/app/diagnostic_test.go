package app

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/store"
)

func diagnosticApp(t *testing.T, nodes []model.Node) (*App, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	if _, err := store.NewGenerationStore(root).Publish([]byte("fixture"), nodes); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return &App{Config: Config{Dir: root}, Credentials: &memoryCredentials{}, Out: out}, out
}

func TestPingIsExplicitlyTCPOnly(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	app, out := diagnosticApp(t, []model.Node{{Name: "tcp", Type: "vmess", Server: address.IP.String(), Port: address.Port}})
	if code := app.Ping(""); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "TCP CONNECT only") || !strings.Contains(out.String(), "REACHABLE") {
		t.Fatalf("missing explicit TCP result: %s", out.String())
	}
}

func TestHealthKeepsOrderAndReturnsNonzero(t *testing.T) {
	nodes := []model.Node{
		{Name: "first", Type: "vmess"},
		{Name: "second", Type: "vmess"},
	}
	app, out := diagnosticApp(t, nodes)
	plane := &refreshPlane{id: "sing-box"}
	plane.probe = func(node model.Node) (dataplane.HealthResult, error) {
		if node.Name == "first" {
			return dataplane.HealthResult{Status: dataplane.HealthHealthy}, nil
		}
		return dataplane.HealthResult{Status: dataplane.HealthUnhealthy}, nil
	}
	registry, err := dataplane.NewRegistry("sing-box", plane)
	if err != nil {
		t.Fatal(err)
	}
	app.DataPlanes = registry
	app.Config.Backend = "sing-box"
	if code := app.Health(""); code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	text := out.String()
	if strings.Index(text, "first") > strings.Index(text, "second") {
		t.Fatalf("node order changed: %s", text)
	}
}

func TestHealthRetriesAfterEarlyCoreExit(t *testing.T) {
	app, _ := diagnosticApp(t, []model.Node{{Name: "node", Type: "vmess"}})
	attempts := 0
	plane := &refreshPlane{id: "sing-box"}
	plane.probe = func(model.Node) (dataplane.HealthResult, error) {
		attempts++
		if attempts == 1 {
			return dataplane.HealthResult{}, dataplane.NewError(
				dataplane.CodeStart, "probe", "sing-box",
				"Data plane exited during the health probe", nil,
			)
		}
		return dataplane.HealthResult{Status: dataplane.HealthHealthy}, nil
	}
	registry, err := dataplane.NewRegistry("sing-box", plane)
	if err != nil {
		t.Fatal(err)
	}
	app.DataPlanes = registry
	app.Config.Backend = "sing-box"

	if code := app.Health(""); code != 0 {
		t.Fatalf("code=%d, want 0", code)
	}
	if attempts != 2 {
		t.Fatalf("probe attempts=%d, want 2", attempts)
	}
}

func TestHealthRequiresDataPlaneRegistry(t *testing.T) {
	app, out := diagnosticApp(t, []model.Node{{Name: "node", Type: "vmess"}})

	if code := app.Health(""); code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	if !strings.Contains(out.String(), "Data plane registry is not configured") {
		t.Fatalf("missing dependency error: %q", out.String())
	}
}
