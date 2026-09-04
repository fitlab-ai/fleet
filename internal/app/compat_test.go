package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/store"
)

func addTestSubscription(t *testing.T, root, id, name, rawURL string, credentials *memoryCredentials) {
	t.Helper()
	registry, err := store.OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(id, name); err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SetURL(id, rawURL); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionAddKeepsCredentialsAndStableIdentity(t *testing.T) {
	root := t.TempDir()
	credentials := &memoryCredentials{}
	var out bytes.Buffer
	app := App{Config: Config{Dir: root}, Credentials: credentials, Out: &out}
	if app.SubscriptionAdd("one", "https://one.example/sub") != 0 ||
		app.SubscriptionAdd("two", "https://two.example/sub") != 0 {
		t.Fatal(out.String())
	}
	registry, err := store.OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Registry.Subscriptions) != 2 {
		t.Fatalf("subscriptions=%d", len(registry.Registry.Subscriptions))
	}
	first, second := registry.Registry.Subscriptions[0], registry.Registry.Subscriptions[1]
	if first.ID == second.ID || credentials.values[first.ID] != "https://one.example/sub" ||
		credentials.values[second.ID] != "https://two.example/sub" {
		t.Fatalf("credentials/identities not distinct: %#v %#v", registry.Registry.Subscriptions, credentials.values)
	}
	if !strings.Contains(out.String(), first.ID[:8]) || !strings.Contains(out.String(), second.ID[:8]) {
		t.Fatalf("stable identities missing from output: %s", out.String())
	}
}

func TestLegacyCacheMigrationCompatibility(t *testing.T) {
	t.Run("uncredentialed cache remains visible after rejected add", func(t *testing.T) {
		root := t.TempDir()
		if err := store.AtomicJSON(filepath.Join(root, "nodes.json"), map[string]any{
			"nodes": []model.Node{{Name: "legacy", Type: "vmess"}},
		}); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		app := App{Config: Config{Dir: root}, Credentials: &memoryCredentials{}, Out: &out}
		if app.SubscriptionAdd("new", "https://new.example/sub") == 0 {
			t.Fatal("add unexpectedly hid uncredentialed legacy cache")
		}
		nodes := app.LoadNodes(false)
		if len(nodes) != 1 || nodes[0].Name != "legacy" {
			t.Fatalf("legacy cache hidden: %#v", nodes)
		}
	})

	t.Run("credentialed legacy cache migrates with its credential", func(t *testing.T) {
		root := t.TempDir()
		if err := store.AtomicJSON(filepath.Join(root, "nodes.json"), map[string]any{
			"nodes": []model.Node{{Name: "legacy", Type: "vmess"}},
		}); err != nil {
			t.Fatal(err)
		}
		credentials := &memoryCredentials{values: map[string]string{"": "https://legacy.example/sub"}}
		app := App{Config: Config{Dir: root}, Credentials: credentials, Out: &bytes.Buffer{}}
		registry, err := app.ensureRegistryMigrated(false)
		if err != nil {
			t.Fatal(err)
		}
		record := registry.Registry.Subscriptions[0]
		if credentials.values[record.ID] != credentials.values[""] {
			t.Fatalf("legacy credential not preserved: %#v", credentials.values)
		}
		nodes := app.LoadNodes(false)
		if len(nodes) != 1 || nodes[0].Fleet.SubscriptionID != record.ID {
			t.Fatalf("legacy cache not attached to migrated identity: %#v", nodes)
		}
	})
}

func TestMigrationReportsSafeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	app := App{Config: Config{Dir: t.TempDir()}, Credentials: &memoryCredentials{}, Out: &out}
	if app.SubscriptionMigrate(path, "https://user:SECRET@example.com/sub", "legacy") == 0 {
		t.Fatal("invalid migration succeeded")
	}
	if strings.Contains(out.String(), "SECRET") || !strings.Contains(out.String(), "Migration failed") {
		t.Fatalf("unsafe migration error: %s", out.String())
	}
}

func TestAggregationPreservesOrderSourceAndDuplicateNames(t *testing.T) {
	root := t.TempDir()
	registry, err := store.OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	for subscriptionIndex := 0; subscriptionIndex < 10; subscriptionIndex++ {
		id := fmt.Sprintf("%032x", subscriptionIndex+1)
		record, err := registry.Add(id, fmt.Sprintf("source-%d", subscriptionIndex))
		if err != nil {
			t.Fatal(err)
		}
		nodes := make([]model.Node, 50)
		for nodeIndex := range nodes {
			nodes[nodeIndex] = model.Node{
				Name: fmt.Sprintf("node-%d", nodeIndex), Type: "vmess",
				Server: "example.com", Port: 443, UUID: fmt.Sprintf("%032d", nodeIndex),
			}
		}
		if _, err := store.NewGenerationStore(filepath.Join(root, "subscriptions", record.ID)).Publish([]byte(record.Name), nodes); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Save(); err != nil {
		t.Fatal(err)
	}
	app := App{Config: Config{Dir: root}, Credentials: &memoryCredentials{}, Out: &bytes.Buffer{}}
	nodes := app.LoadNodes(false)
	if len(nodes) != 500 {
		t.Fatalf("nodes=%d, want 500", len(nodes))
	}
	if nodes[0].Name != "node-0" || nodes[50].Name != "node-0" ||
		nodes[0].Fleet.NodeKey == nodes[50].Fleet.NodeKey ||
		nodes[0].Fleet.SubscriptionName == nodes[50].Fleet.SubscriptionName {
		t.Fatal("subscription identity was not preserved")
	}
}

func TestRemoveRetainsCacheUntilRefreshPurge(t *testing.T) {
	root := t.TempDir()
	credentials := &memoryCredentials{}
	id := strings.Repeat("a", 32)
	addTestSubscription(t, root, id, "source", "https://source.example/sub", credentials)
	subRoot := subscriptionRoot(root, id)
	if _, err := store.NewGenerationStore(subRoot).Publish([]byte("old"), []model.Node{{
		Name: "cached", Type: "vmess", Server: "example.com", Port: 443, UUID: "id",
	}}); err != nil {
		t.Fatal(err)
	}
	app := App{Config: Config{Dir: root}, Credentials: credentials, Out: &bytes.Buffer{}}
	if app.SubscriptionRemove("source") != 0 {
		t.Fatal("remove failed")
	}
	nodes := app.LoadNodes(false)
	if len(nodes) != 1 || nodes[0].Fleet.SubscriptionStatus != "removed" {
		t.Fatalf("removed cache was not retained/marked: %#v", nodes)
	}
	if app.Refresh("", false) != 0 {
		t.Fatal("purge refresh failed")
	}
	if _, err := os.Stat(subRoot); !os.IsNotExist(err) {
		t.Fatalf("removed cache was not purged: %v", err)
	}
}

func TestRefreshIsolatesFailuresAndPublishesCounts(t *testing.T) {
	root := t.TempDir()
	credentials := &memoryCredentials{}
	goodID, badID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	addTestSubscription(t, root, goodID, "good", "https://good.example/sub", credentials)
	addTestSubscription(t, root, badID, "bad", "https://bad.example/sub", credentials)
	badRoot := subscriptionRoot(root, badID)
	if _, err := store.NewGenerationStore(badRoot).Publish([]byte("old"), []model.Node{{
		Name: "old", Type: "vmess", Server: "old.example", Port: 443, UUID: "old",
	}}); err != nil {
		t.Fatal(err)
	}
	app := App{Config: Config{Dir: root}, Credentials: credentials, Out: &bytes.Buffer{}}
	app.download = func(rawURL string) ([]byte, error) {
		if strings.Contains(rawURL, "bad.") {
			return nil, errors.New("download failed")
		}
		return []byte("proxies:\n  - {name: trojan, type: trojan, server: good.example, port: 443, password: p}\n"), nil
	}
	app.DataPlanes = testDataPlanes(t)
	app.Config.Backend = "sing-box"
	if app.Refresh("", false) != 1 {
		t.Fatal("partial failure must return nonzero")
	}
	badNodes, _ := store.NewGenerationStore(badRoot).LoadNodes()
	if len(badNodes) != 1 || badNodes[0].Name != "old" {
		t.Fatalf("failed cache was not preserved: %#v", badNodes)
	}
	state := readRecordState(root, goodID)
	if state.NodeCount != 1 || state.ProtocolCounts["trojan"] != 1 || state.LastSuccess == "" {
		t.Fatalf("successful subscription state incomplete: %#v", state)
	}
}

func TestRefreshIsolatesPerSubscriptionDiskFailure(t *testing.T) {
	root := t.TempDir()
	credentials := &memoryCredentials{}
	firstID, secondID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	addTestSubscription(t, root, firstID, "first", "https://first.example/sub", credentials)
	addTestSubscription(t, root, secondID, "second", "https://second.example/sub", credentials)
	app := App{Config: Config{Dir: root}, Credentials: credentials, Out: &bytes.Buffer{}}
	app.download = func(string) ([]byte, error) {
		return []byte("proxies:\n  - {name: node, type: vmess, server: example.com, port: 443, uuid: id}\n"), nil
	}
	app.DataPlanes = testDataPlanes(t)
	app.Config.Backend = "sing-box"
	app.publish = func(target string, source []byte, nodes []model.Node) (string, error) {
		if strings.Contains(target, firstID) {
			return "", errors.New("disk full")
		}
		return store.NewGenerationStore(target).Publish(source, nodes)
	}
	if app.Refresh("", false) != 1 {
		t.Fatal("disk failure must produce partial failure")
	}
	if state := readRecordState(root, secondID); state.LastSuccess == "" || state.NodeCount != 1 {
		t.Fatalf("second subscription was not published: %#v", state)
	}
}

func TestRefreshValidatesBeforePublishingAndForceDoesNotBypassProtocol(t *testing.T) {
	root := t.TempDir()
	credentials := &memoryCredentials{}
	id := strings.Repeat("a", 32)
	addTestSubscription(t, root, id, "source", "https://source.example/sub", credentials)
	published := false
	app := App{Config: Config{Dir: root}, Credentials: credentials, Out: &bytes.Buffer{}}
	app.download = func(string) ([]byte, error) {
		return []byte("proxies:\n  - {name: invalid, type: future, server: example.com, port: 443, password: secret}\n"), nil
	}
	app.publish = func(string, []byte, []model.Node) (string, error) {
		published = true
		return "generation", nil
	}
	if app.Refresh("", true) != 1 || published {
		t.Fatal("force bypassed protocol validation or invalid nodes were published")
	}
}

type failingDeleteCredentials struct{ memoryCredentials }

func (f *failingDeleteCredentials) DeleteURL(string) error { return fmt.Errorf("delete failed") }

func TestSubscriptionRemoveRollsBackCredentialFailure(t *testing.T) {
	root := t.TempDir()
	credentials := &failingDeleteCredentials{}
	credentials.values = map[string]string{}
	var out bytes.Buffer
	app := App{Config: Config{Dir: root}, Credentials: credentials, Out: &out}
	if app.SubscriptionAdd("source", "https://example.com/sub") != 0 {
		t.Fatal(out.String())
	}
	if app.SubscriptionRemove("source") == 0 {
		t.Fatal("credential deletion failure was ignored")
	}
	registry, err := store.OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Registry.Subscriptions) != 1 || registry.Registry.Subscriptions[0].Status != "active" {
		t.Fatalf("registry was not rolled back: %#v", registry.Registry.Subscriptions)
	}
	if strings.Contains(out.String(), "https://") {
		t.Fatal("credential leaked")
	}
}
