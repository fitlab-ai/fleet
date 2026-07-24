package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fitlab-ai/fleet/internal/model"
)

func testNode(name string) model.Node {
	return model.Node{Name: name, Type: "vmess", Server: "example.com", Port: 443, UUID: "00000000-0000-0000-0000-000000000001"}
}

func TestAtomicJSONUsesSecureMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if err := AtomicJSON(path, map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%o, want 600", got)
	}
	if got := infoDirMode(t, filepath.Dir(path)); got != 0o700 {
		t.Fatalf("dir mode=%o, want 700", got)
	}
}

func infoDirMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestGenerationFallsBackWhenCurrentIsDamaged(t *testing.T) {
	root := t.TempDir()
	s := NewGenerationStore(root)
	if _, err := s.Publish([]byte("one"), []model.Node{testNode("old")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish([]byte("two"), []model.Node{testNode("new")}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "current.json"))
	var current struct {
		Generation string `json:"generation"`
	}
	_ = json.Unmarshal(raw, &current)
	if err := os.WriteFile(filepath.Join(root, "generations", current.Generation, "nodes.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.LoadNodes()
	if err != nil || len(nodes) != 1 || nodes[0].Name != "old" {
		t.Fatalf("fallback=%#v, err=%v", nodes, err)
	}
}

func TestPIDLockRejectsCurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writer.lock")
	if err := os.WriteFile(path, []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := PIDLock{Path: path}
	if err := lock.Acquire(); err == nil {
		t.Fatal("live process lock was accepted")
	}
}
