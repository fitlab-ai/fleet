package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/platform"
	"github.com/fitlab-ai/fleet/internal/store"
)

const (
	SchemaV1Legacy uint32 = 1
	SchemaV2       uint32 = 2
)

type Phase string

const (
	PhasePreparing Phase = "preparing"
	PhaseActive    Phase = "active"
	PhaseReleasing Phase = "releasing"
	PhaseDegraded  Phase = "degraded"
)

type ClaimStatus string

const (
	ClaimReserved ClaimStatus = "reserved"
	ClaimActive   ClaimStatus = "active"
	ClaimReleased ClaimStatus = "released"
	ClaimFailed   ClaimStatus = "failed"
)

type Claim struct {
	Kind      dataplane.ResourceKind `json:"kind"`
	Key       string                 `json:"key"`
	Owner     string                 `json:"owner,omitempty"`
	ManagedBy string                 `json:"managed_by,omitempty"`
	Status    ClaimStatus            `json:"status"`
}

type NodeRef struct {
	Name             string `json:"name"`
	Key              string `json:"key,omitempty"`
	SubscriptionID   string `json:"subscription_id,omitempty"`
	SubscriptionName string `json:"subscription_name,omitempty"`
}

type State struct {
	Schema         uint32                    `json:"schema,omitempty"`
	Phase          Phase                     `json:"phase,omitempty"`
	LeaseID        string                    `json:"lease_id,omitempty"`
	Instance       dataplane.Instance        `json:"instance,omitempty"`
	Node           NodeRef                   `json:"node_ref,omitempty"`
	Mode           dataplane.Mode            `json:"mode,omitempty"`
	Port           int                       `json:"port,omitempty"`
	Claims         []Claim                   `json:"claims,omitempty"`
	ResourceBefore platform.ResourceSnapshot `json:"resource_before,omitempty"`
	LastError      string                    `json:"last_error,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at,omitempty"`

	NodeName          string                 `json:"node,omitempty"`
	NodeKey           string                 `json:"node_key,omitempty"`
	SubscriptionID    string                 `json:"subscription_id,omitempty"`
	SubscriptionName  string                 `json:"subscription_name,omitempty"`
	PID               int                    `json:"pid,omitempty"`
	SystemProxyBefore platform.ProxySnapshot `json:"system_proxy_before,omitempty"`

	Legacy   bool `json:"-"`
	ReadOnly bool `json:"-"`
}

type StateStore struct{ Path string }

func NewStateStore(path string) *StateStore { return &StateStore{Path: path} }

func (s *StateStore) Load() (*State, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var header struct {
		Schema uint32 `json:"schema"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, dataplane.NewError(
			dataplane.CodeStateCorrupt, "state-load", "",
			"Fleet runtime state is invalid", err,
		)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, dataplane.NewError(
			dataplane.CodeStateCorrupt, "state-load", "",
			"Fleet runtime state is invalid", err,
		)
	}
	switch header.Schema {
	case 0:
		state.Schema, state.Legacy = SchemaV1Legacy, true
		state.Node = NodeRef{
			Name: state.NodeName, Key: state.NodeKey,
			SubscriptionID: state.SubscriptionID, SubscriptionName: state.SubscriptionName,
		}
		state.Instance = dataplane.Instance{
			Backend: "sing-box", Mode: state.Mode,
			Process: dataplane.ProcessIdentity{PID: state.PID},
		}
	case SchemaV2:
	default:
		state.ReadOnly = true
	}
	return &state, nil
}

func (s *StateStore) Save(state *State) error {
	if state == nil {
		return fmt.Errorf("runtime state is required")
	}
	if state.ReadOnly || state.Schema > SchemaV2 {
		return dataplane.NewError(
			dataplane.CodeStateCorrupt, "state-save", state.Instance.Backend,
			"Fleet runtime state was created by a newer version", nil,
		)
	}
	state.Schema = SchemaV2
	state.UpdatedAt = time.Now().UTC()
	state.Mode = state.Instance.Mode
	state.PID = state.Instance.Process.PID
	state.NodeName = state.Node.Name
	state.NodeKey = state.Node.Key
	state.SubscriptionID = state.Node.SubscriptionID
	state.SubscriptionName = state.Node.SubscriptionName
	return store.AtomicJSON(s.Path, state)
}

func (s *StateStore) Remove() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (state *State) ReconcileLegacy(inspector platform.ProcessInspector, configDir, binary string) error {
	if !state.Legacy || state.Instance.Process.PID <= 0 {
		return nil
	}
	current, err := inspector.Inspect(state.Instance.Process.PID)
	if err != nil {
		return dataplane.NewError(
			dataplane.CodeOwnershipUnknown, "legacy-reconcile", "sing-box",
			"Legacy process ownership could not be verified", err,
		)
	}
	configPath := filepath.Join(configDir, "sing-box.json")
	expected := dataplane.ProcessIdentity{
		PID: state.Instance.Process.PID, Executable: binary,
		ArgsFingerprint: platform.FingerprintArgs([]string{
			binary, "run", "-c", configPath, "-D", configDir,
		}),
	}
	if !platform.SameProcess(current, expected) {
		return dataplane.NewError(
			dataplane.CodeOwnershipUnknown, "legacy-reconcile", "sing-box",
			"Legacy process ownership could not be verified", nil,
		)
	}
	state.Instance.Process = current
	state.Instance.Backend = "sing-box"
	state.Instance.Mode = state.Mode
	state.Legacy = false
	state.Schema = SchemaV2
	return nil
}
