package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fitlab-ai/fleet/internal/model"
)

type Manifest struct {
	Generation     string         `json:"generation"`
	CreatedAt      string         `json:"created_at"`
	SourceSHA256   string         `json:"source_sha256"`
	NodesSHA256    string         `json:"nodes_sha256"`
	NodeCount      int            `json:"node_count"`
	ProtocolCounts map[string]int `json:"protocol_counts"`
}

type GenerationStore struct{ Root string }

func NewGenerationStore(root string) *GenerationStore { return &GenerationStore{Root: root} }

func sum(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

func protocolCounts(nodes []model.Node) map[string]int {
	out := map[string]int{"vmess": 0, "hysteria2": 0, "anytls": 0, "trojan": 0}
	for _, node := range nodes {
		out[node.Type]++
	}
	return out
}

func (s *GenerationStore) Publish(source []byte, nodes []model.Node) (string, error) {
	generation := fmt.Sprintf("%d-%x", time.Now().UTC().UnixNano(), sha256.Sum256(source))[:32]
	dir := filepath.Join(s.Root, "generations", generation)
	if err := SecureDir(dir); err != nil {
		return "", err
	}
	nodesData, err := json.MarshalIndent(map[string]any{"nodes": nodes}, "", "  ")
	if err != nil {
		return "", err
	}
	nodesData = append(nodesData, '\n')
	if err := AtomicWrite(filepath.Join(dir, "source.yaml"), source); err != nil {
		return "", err
	}
	if err := AtomicWrite(filepath.Join(dir, "nodes.json"), nodesData); err != nil {
		return "", err
	}
	manifest := Manifest{
		Generation: generation, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		SourceSHA256: sum(source), NodesSHA256: sum(nodesData), NodeCount: len(nodes),
		ProtocolCounts: protocolCounts(nodes),
	}
	if err := AtomicJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return "", err
	}
	if err := AtomicJSON(filepath.Join(s.Root, "current.json"), map[string]string{"generation": generation}); err != nil {
		return "", err
	}
	return generation, nil
}

func (s *GenerationStore) generationNames() []string {
	entries, _ := os.ReadDir(filepath.Join(s.Root, "generations"))
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names
}

func (s *GenerationStore) loadGeneration(name string) ([]model.Node, error) {
	dir := filepath.Join(s.Root, "generations", name)
	var manifest Manifest
	if err := ReadJSON(filepath.Join(dir, "manifest.json"), &manifest); err != nil {
		return nil, err
	}
	for kind, count := range manifest.ProtocolCounts {
		switch kind {
		case "vmess", "hysteria2", "anytls", "trojan":
			if count < 0 {
				return nil, fmt.Errorf("invalid protocol count")
			}
		default:
			if count != 0 {
				return nil, fmt.Errorf("unknown protocol count")
			}
		}
	}
	sourceData, err := os.ReadFile(filepath.Join(dir, "source.yaml"))
	if err != nil || sum(sourceData) != manifest.SourceSHA256 {
		return nil, fmt.Errorf("source hash mismatch")
	}
	nodesData, err := os.ReadFile(filepath.Join(dir, "nodes.json"))
	if err != nil || sum(nodesData) != manifest.NodesSHA256 {
		return nil, fmt.Errorf("nodes hash mismatch")
	}
	var payload struct {
		Nodes []model.Node `json:"nodes"`
	}
	if err := json.Unmarshal(nodesData, &payload); err != nil {
		return nil, err
	}
	if len(payload.Nodes) != manifest.NodeCount {
		return nil, fmt.Errorf("node count mismatch")
	}
	return payload.Nodes, nil
}

func (s *GenerationStore) LoadNodes() ([]model.Node, error) {
	var current struct {
		Generation string `json:"generation"`
	}
	_ = ReadJSON(filepath.Join(s.Root, "current.json"), &current)
	candidates := []string{}
	if current.Generation != "" {
		candidates = append(candidates, current.Generation)
	}
	for _, name := range s.generationNames() {
		if name != current.Generation {
			candidates = append(candidates, name)
		}
	}
	for _, name := range candidates {
		if nodes, err := s.loadGeneration(name); err == nil {
			return nodes, nil
		}
	}
	var legacy struct {
		Nodes []model.Node `json:"nodes"`
	}
	if err := ReadJSON(filepath.Join(s.Root, "nodes.json"), &legacy); err == nil {
		return legacy.Nodes, nil
	}
	return []model.Node{}, nil
}
