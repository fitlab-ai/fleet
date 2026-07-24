package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fitlab-ai/fleet/internal/backend"
	"github.com/fitlab-ai/fleet/internal/credential"
	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/store"
	"github.com/fitlab-ai/fleet/internal/subscription"
)

type Config struct {
	Dir         string
	SingBox     string
	ClashConfig string
	Port        int
	Timeout     time.Duration
}

type App struct {
	Config      Config
	Credentials credential.Backend
	Out         io.Writer
	In          io.Reader
}

func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	port := 7890
	if value := os.Getenv("FLEET_PORT"); value != "" {
		if _, err := fmt.Sscan(value, &port); err != nil {
			port = 7890
		}
	}
	timeout := 30 * time.Second
	if value := os.Getenv("FLEET_SUBSCRIPTION_TIMEOUT"); value != "" {
		var seconds int
		if _, err := fmt.Sscan(value, &seconds); err == nil && seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}
	return Config{
		Dir:         filepath.Join(home, ".config", "fleet"),
		SingBox:     "/opt/homebrew/bin/sing-box",
		ClashConfig: filepath.Join(home, "Library", "Application Support", "com.follow.clash", "config.yaml"),
		Port:        port, Timeout: timeout,
	}
}

func (a *App) printf(format string, args ...any) { fmt.Fprintf(a.Out, format, args...) }
func safeCategory(err error, fallback string) string {
	if typed, ok := err.(*model.SafeError); ok {
		return typed.Category
	}
	return fallback
}

func newID() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func subscriptionRoot(configDir, id string) string {
	return filepath.Join(configDir, "subscriptions", id)
}

func (a *App) registry() (*store.RegistryStore, error) {
	return store.OpenRegistry(a.Config.Dir)
}

func (a *App) ensureRegistryMigrated(allowUncredentialed bool) (*store.RegistryStore, error) {
	registry, err := a.registry()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(registry.Path); err == nil {
		return registry, nil
	}
	legacy := false
	for _, name := range []string{"current.json", "nodes.json", "generations", "subscription-state.json"} {
		if _, statErr := os.Stat(filepath.Join(a.Config.Dir, name)); statErr == nil {
			legacy = true
			break
		}
	}
	if !legacy {
		return registry, nil
	}
	legacyURL, credentialErr := a.Credentials.GetURL("")
	if credentialErr != nil && !allowUncredentialed {
		return nil, model.NewError("migration", "Legacy cache has no credential; run 'fleet subscription migrate --url URL'", nil)
	}
	record, err := registry.Add(newID(), "")
	if err != nil {
		return nil, err
	}
	if credentialErr == nil {
		if err := a.Credentials.SetURL(record.ID, legacyURL); err != nil {
			return nil, err
		}
	}
	target := subscriptionRoot(a.Config.Dir, record.ID)
	if err := copyLegacyStore(a.Config.Dir, target); err != nil {
		if credentialErr == nil {
			_ = a.Credentials.DeleteURL(record.ID)
		}
		return nil, err
	}
	if err := registry.Save(); err != nil {
		if credentialErr == nil {
			_ = a.Credentials.DeleteURL(record.ID)
		}
		return nil, err
	}
	return registry, nil
}

func copyLegacyStore(root, target string) error {
	if err := store.SecureDir(target); err != nil {
		return err
	}
	for _, name := range []string{"current.json", "nodes.json", "subscription-state.json"} {
		source := filepath.Join(root, name)
		data, err := os.ReadFile(source)
		if err == nil {
			if err := store.AtomicWrite(filepath.Join(target, name), data); err != nil {
				return err
			}
		}
	}
	sourceGenerations := filepath.Join(root, "generations")
	return filepath.WalkDir(sourceGenerations, func(path string, entry os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(sourceGenerations, path)
		destination := filepath.Join(target, "generations", relative)
		if entry.IsDir() {
			return store.SecureDir(destination)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return store.AtomicWrite(destination, data)
	})
}

func (a *App) LoadNodes(warn bool) []model.Node {
	registryPath := filepath.Join(a.Config.Dir, "subscriptions.json")
	if _, err := os.Stat(registryPath); err != nil {
		nodes, _ := store.NewGenerationStore(a.Config.Dir).LoadNodes()
		return nodes
	}
	registry, err := a.ensureRegistryMigrated(false)
	if err != nil {
		if warn {
			a.printf("Warning: %s\n", err)
		}
		return []model.Node{}
	}
	var aggregated []model.Node
	for _, record := range registry.Registry.Subscriptions {
		nodes, _ := store.NewGenerationStore(subscriptionRoot(a.Config.Dir, record.ID)).LoadNodes()
		if len(nodes) == 0 && warn {
			a.printf("Warning: no usable cache for subscription %s\n", record.Name)
		}
		for _, node := range nodes {
			node.Fleet = model.Metadata{
				NodeKey: record.ID + "/" + node.Name, SubscriptionID: record.ID,
				SubscriptionName: record.Name, SubscriptionStatus: record.Status,
			}
			aggregated = append(aggregated, node)
		}
	}
	return aggregated
}

func (a *App) List() int {
	nodes := a.LoadNodes(true)
	a.printf("%-4s %-30s %-20s %-8s %-10s %s\n", "", "NODE", "SOURCE", "STATE", "TYPE", "SERVER")
	a.printf("%s\n", strings.Repeat("-", 80))
	for i, node := range nodes {
		source, status := node.Fleet.SubscriptionName, node.Fleet.SubscriptionStatus
		if source == "" {
			source, status = "legacy", "active"
		}
		a.printf("  %2d %-30s %-20s %-8s %-10s %-30s\n", i, node.Name, source, status, node.Type, node.Server)
	}
	a.printf("%s\nTotal: %d nodes\n", strings.Repeat("-", 80), len(nodes))
	state, _ := a.LoadState()
	if state != nil {
		a.printf("Mode: %s  |  Node: %s  |  PID: %d\n", state.Mode, state.NodeKey, state.PID)
	} else {
		a.printf("Not running\n")
	}
	return 0
}

func (a *App) Export(target, mode string) int {
	node, err := model.ResolveNode(a.LoadNodes(true), target)
	if err != nil {
		a.printf("%s\n", err)
		return 1
	}
	config, err := backend.Export(node, mode, a.Config.Port)
	if err != nil {
		a.printf("%s\n", err)
		return 1
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	a.printf("%s\n", data)
	return 0
}

func (a *App) SubscriptionAdd(name, rawURL string) int {
	if rawURL == "" && a.In != nil {
		fmt.Fscanln(a.In, &rawURL)
	}
	value, err := subscription.ValidateURL(rawURL)
	if err != nil {
		a.printf("Subscription add failed [%s]: %s\n", safeCategory(err, "credential"), err)
		return 1
	}
	lock := store.NewCompositeLock(a.Config.Dir)
	if err := lock.Acquire(); err != nil {
		a.printf("Subscription add failed [%s]: %s\n", safeCategory(err, "lock"), err)
		return 1
	}
	defer lock.Release()
	registry, err := a.ensureRegistryMigrated(false)
	if err != nil {
		a.printf("Subscription add failed [%s]: %s\n", safeCategory(err, "registry"), err)
		return 1
	}
	record, err := registry.Add(newID(), name)
	if err == nil {
		err = a.Credentials.SetURL(record.ID, value)
	}
	if err == nil {
		err = registry.Save()
		if err != nil {
			_ = a.Credentials.DeleteURL(record.ID)
		}
	}
	if err != nil {
		a.printf("Subscription add failed [%s]: %s\n", safeCategory(err, "registry"), err)
		return 1
	}
	a.printf("✓ Subscription added: %s (ID: %s)\n", record.Name, record.ID[:8])
	return 0
}

type RecordState struct {
	Schema         int            `json:"schema"`
	LastAttempt    string         `json:"last_attempt,omitempty"`
	LastSuccess    string         `json:"last_success,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
	Generation     string         `json:"generation,omitempty"`
	NodeCount      int            `json:"node_count"`
	ProtocolCounts map[string]int `json:"protocol_counts,omitempty"`
}

func readRecordState(configDir, id string) RecordState {
	var state RecordState
	_ = store.ReadJSON(filepath.Join(subscriptionRoot(configDir, id), "state.json"), &state)
	return state
}

func writeRecordState(configDir, id string, state RecordState) error {
	return store.AtomicJSON(filepath.Join(subscriptionRoot(configDir, id), "state.json"), state)
}

func (a *App) SubscriptionStatus(selector string) int {
	if _, err := os.Stat(filepath.Join(a.Config.Dir, "subscriptions.json")); err != nil {
		configured := a.Credentials.IsConfigured("")
		a.printf("Configured: %s\n", map[bool]string{true: "yes", false: "no"}[configured])
		return 0
	}
	registry, err := a.ensureRegistryMigrated(false)
	if err != nil {
		a.printf("Subscription status failed [%s]: %s\n", safeCategory(err, "registry"), err)
		return 1
	}
	records := registry.Registry.Subscriptions
	if selector != "" {
		record, resolveErr := registry.Resolve(selector, false)
		if resolveErr != nil {
			a.printf("Subscription status failed [%s]: %s\n", safeCategory(resolveErr, "selector"), resolveErr)
			return 1
		}
		records = []model.Subscription{*record}
	} else {
		active := 0
		for _, record := range records {
			if record.Status == "active" {
				active++
			}
		}
		a.printf("Subscriptions: %d (%d active)\n", len(records), active)
	}
	for _, record := range records {
		state := readRecordState(a.Config.Dir, record.ID)
		configured := record.Status == "active" && a.Credentials.IsConfigured(record.ID)
		a.printf("%s (%s): %s, credential=%s, nodes=%d\n", record.Name, record.ID[:8], record.Status,
			map[bool]string{true: "yes", false: "no"}[configured], state.NodeCount)
		if state.LastSuccess != "" {
			a.printf("  Last success: %s\n", state.LastSuccess)
		}
		if state.LastError != "" {
			a.printf("  Last error: %s\n", state.LastError)
		}
	}
	return 0
}

func (a *App) SubscriptionRemove(selector string) int {
	lock := store.NewCompositeLock(a.Config.Dir)
	if err := lock.Acquire(); err != nil {
		a.printf("Subscription remove failed [%s]: %s\n", safeCategory(err, "lock"), err)
		return 1
	}
	defer lock.Release()
	registry, err := a.ensureRegistryMigrated(false)
	if err != nil {
		a.printf("Subscription remove failed [%s]: %s\n", safeCategory(err, "registry"), err)
		return 1
	}
	record, err := registry.MarkRemoved(selector)
	if err == nil {
		err = registry.Save()
	}
	if err == nil {
		err = a.Credentials.DeleteURL(record.ID)
		if err != nil {
			registry.RestoreActive(record.ID)
			_ = registry.Save()
		}
	}
	if err != nil {
		a.printf("Subscription remove failed [%s]: %s\n", safeCategory(err, "registry"), err)
		return 1
	}
	a.printf("✓ Subscription removed: %s; cached nodes retained [removed]\n", record.Name)
	a.printf("Removal cannot be undone; run fleet refresh to purge, then add the subscription again.\n")
	return 0
}

func (a *App) Refresh(selector string, force bool) int {
	lock := store.NewCompositeLock(a.Config.Dir)
	if err := lock.Acquire(); err != nil {
		a.printf("Refresh failed [%s]: %s\n", safeCategory(err, "lock"), err)
		return 1
	}
	defer lock.Release()
	registry, err := a.ensureRegistryMigrated(false)
	if err != nil {
		a.printf("Refresh failed [%s]: %s\n", safeCategory(err, "registry"), err)
		return 1
	}
	var targets []model.Subscription
	if selector != "" {
		record, resolveErr := registry.Resolve(selector, true)
		if resolveErr != nil {
			a.printf("Refresh failed [%s]: %s\n", safeCategory(resolveErr, "selector"), resolveErr)
			return 1
		}
		targets = []model.Subscription{*record}
	} else {
		for _, record := range registry.Registry.Subscriptions {
			if record.Status == "active" {
				targets = append(targets, record)
			}
		}
	}
	successes, failures := 0, 0
	for _, record := range targets {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		state := readRecordState(a.Config.Dir, record.ID)
		rawURL, itemErr := a.Credentials.GetURL(record.ID)
		var source []byte
		if itemErr == nil {
			source, itemErr = (subscription.Downloader{Timeout: a.Config.Timeout}).Download(rawURL)
		}
		var nodes []model.Node
		var counts map[string]int
		if itemErr == nil {
			nodes, itemErr = subscription.ParseYAML(source)
		}
		if itemErr == nil {
			nodes, counts, itemErr = subscription.ValidateNodes(nodes)
		}
		if itemErr == nil {
			itemErr = subscription.EnforceNodeCount(len(nodes), state.NodeCount, force)
		}
		if itemErr == nil {
			itemErr = (backend.SingBox{Binary: a.Config.SingBox}).ValidateNodes(nodes, a.Config.Port)
		}
		if itemErr == nil {
			var generation string
			generation, itemErr = store.NewGenerationStore(subscriptionRoot(a.Config.Dir, record.ID)).Publish(source, nodes)
			if itemErr == nil {
				state = RecordState{Schema: 1, LastAttempt: now, LastSuccess: now, Generation: generation, NodeCount: len(nodes), ProtocolCounts: counts}
				itemErr = writeRecordState(a.Config.Dir, record.ID, state)
			}
		}
		if itemErr != nil {
			state.Schema, state.LastAttempt, state.LastError = 1, now, safeCategory(itemErr, "cache")
			_ = writeRecordState(a.Config.Dir, record.ID, state)
			failures++
			a.printf("✗ %s [%s]: %s\n", record.Name, state.LastError, itemErr)
		} else {
			successes++
			a.printf("✓ %s: refreshed %d nodes\n", record.Name, len(nodes))
		}
	}
	removed := registry.PurgeRemoved()
	if len(removed) > 0 {
		_ = registry.Save()
		for _, record := range removed {
			_ = os.RemoveAll(subscriptionRoot(a.Config.Dir, record.ID))
			_ = a.Credentials.DeleteURL(record.ID)
		}
	} else if _, err := os.Stat(registry.Path); err != nil {
		_ = registry.Save()
	}
	a.printf("Refresh complete: %d succeeded, %d failed\n", successes, failures)
	if failures > 0 {
		return 1
	}
	return 0
}

func (a *App) SubscriptionMigrate(sourcePath, rawURL, name string) int {
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		a.printf("Migration failed [migration]: source could not be imported\n")
		return 1
	}
	nodes, err := subscription.ParseYAML(source)
	var counts map[string]int
	if err == nil {
		nodes, counts, err = subscription.ValidateNodes(nodes)
	}
	if err == nil && (len(nodes) != 44 || counts["vmess"] != 29 || counts["hysteria2"] != 4 || counts["anytls"] != 11 || counts["trojan"] != 0) {
		err = model.NewError("migration", "Source does not match the 44-node migration baseline", nil)
	}
	if rawURL == "" {
		rawURL = subscription.RecoverURL(source)
	}
	value, urlErr := subscription.ValidateURL(rawURL)
	if err == nil && urlErr != nil {
		err = model.NewError("credential", "Subscription URL could not be recovered; use --url", nil)
	}
	if err == nil {
		err = (backend.SingBox{Binary: a.Config.SingBox}).ValidateNodes(nodes, a.Config.Port)
	}
	if err != nil {
		a.printf("Migration failed [%s]: %s\n", safeCategory(err, "migration"), err)
		return 1
	}
	lock := store.NewCompositeLock(a.Config.Dir)
	if err := lock.Acquire(); err != nil {
		a.printf("Migration failed [%s]: source could not be imported\n", safeCategory(err, "lock"))
		return 1
	}
	defer lock.Release()
	registry, err := a.ensureRegistryMigrated(true)
	if err != nil {
		a.printf("Migration failed [%s]: source could not be imported\n", safeCategory(err, "registry"))
		return 1
	}
	record, err := registry.Add(newID(), name)
	if err == nil {
		err = a.Credentials.SetURL(record.ID, value)
	}
	var generation string
	if err == nil {
		generation, err = store.NewGenerationStore(subscriptionRoot(a.Config.Dir, record.ID)).Publish(source, nodes)
	}
	if err == nil {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		err = writeRecordState(a.Config.Dir, record.ID, RecordState{Schema: 1, LastAttempt: now, LastSuccess: now, Generation: generation, NodeCount: 44, ProtocolCounts: counts})
	}
	if err == nil {
		err = registry.Save()
	}
	if err != nil {
		_ = a.Credentials.DeleteURL(record.ID)
		_ = os.RemoveAll(subscriptionRoot(a.Config.Dir, record.ID))
		a.printf("Migration failed [%s]: source could not be imported\n", safeCategory(err, "migration"))
		return 1
	}
	a.printf("✓ Migrated %s (%s): 44 nodes (vmess: 29, hysteria2: 4, anytls: 11, trojan: 0)\n", record.Name, record.ID[:8])
	return 0
}

func (a *App) LoadState() (*model.RuntimeState, error) {
	var state model.RuntimeState
	if err := store.ReadJSON(filepath.Join(a.Config.Dir, "state.json"), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (a *App) SortedNodes() []model.Node {
	nodes := a.LoadNodes(false)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}
