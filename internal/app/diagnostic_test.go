package app

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

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
	app.healthProbe = func(node model.Node) (string, int64) {
		if node.Name == "first" {
			return "HEALTHY", 1
		}
		return "UNHEALTHY", 2
	}
	if code := app.Health(""); code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	text := out.String()
	if strings.Index(text, "first") > strings.Index(text, "second") {
		t.Fatalf("node order changed: %s", text)
	}
}

func TestHealthRetriesAfterEarlyCoreExit(t *testing.T) {
	app := &App{}
	attempts := 0
	app.healthTry = func(model.Node) (string, int64, bool) {
		attempts++
		if attempts == 1 {
			return "START_FAILED", -1, true
		}
		return "HEALTHY", 7, false
	}
	status, elapsed := app.probeHealth(model.Node{Name: "node"})
	if status != "HEALTHY" || elapsed != 7 || attempts != 2 {
		t.Fatalf("status=%s elapsed=%d attempts=%d", status, elapsed, attempts)
	}
}

func TestHealthReportsConfigWriteFailure(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Config: Config{SingBox: binary}}
	app.writeFile = func(string, []byte, os.FileMode) error {
		return errors.New("disk full")
	}
	status, elapsed, retry := app.probeHealthAttempt(model.Node{Name: "node"})
	if status != "CONFIG_ERROR" || elapsed != -1 || retry {
		t.Fatalf("status=%s elapsed=%d retry=%v", status, elapsed, retry)
	}
}
