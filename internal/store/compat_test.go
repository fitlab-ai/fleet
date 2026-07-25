package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/model"
)

func TestGenerationCompatibility(t *testing.T) {
	root := t.TempDir()
	store := NewGenerationStore(root)
	if _, err := store.Publish([]byte("source"), []model.Node{testNode("published")}); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.LoadNodes()
	if err != nil || len(nodes) != 1 || nodes[0].Name != "published" {
		t.Fatalf("published generation did not load: %#v %v", nodes, err)
	}

	var pointer struct {
		Generation string `json:"generation"`
	}
	if err := ReadJSON(filepath.Join(root, "current.json"), &pointer); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "generations", pointer.Generation, "manifest.json")
	var manifest Manifest
	if err := ReadJSON(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest.ProtocolCounts, "trojan")
	manifest.ProtocolCounts["future"] = 1
	if err := AtomicJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{"nodes": []model.Node{testNode("legacy")}}
	if err := AtomicJSON(filepath.Join(root, "nodes.json"), legacy); err != nil {
		t.Fatal(err)
	}
	nodes, err = store.LoadNodes()
	if err != nil || len(nodes) != 1 || nodes[0].Name != "legacy" {
		t.Fatalf("unknown non-zero protocol did not fall back: %#v %v", nodes, err)
	}
}

func TestRegistryCompatibility(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	id1, id2 := strings.Repeat("a", 32), strings.Repeat("b", 32)
	if _, err := registry.Add(id1, "Airport"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(id2, "airport"); err == nil {
		t.Fatal("case-insensitive duplicate name was accepted")
	}
	if _, err := registry.MarkRemoved("Airport"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(id2, "AIRPORT"); err == nil {
		t.Fatal("removed name was reused before purge")
	}
	if removed := registry.PurgeRemoved(); len(removed) != 1 {
		t.Fatalf("removed=%d, want 1", len(removed))
	}
	if _, err := registry.Add(id2, "airport"); err != nil {
		t.Fatalf("purged name could not be reused: %v", err)
	}

	damaged := model.Registry{Schema: 2, Subscriptions: []model.Subscription{{
		ID: id1, Name: "bad", Status: "active",
	}}}
	data, _ := json.Marshal(damaged)
	data = append(data[:len(data)-1], []byte(`,"url":"https://user:secret@example.com"}}`)...)
	path := filepath.Join(root, "subscriptions.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err := OpenRegistry(root); err == nil {
		t.Fatal("damaged registry was accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("damaged registry was overwritten")
	}
}
