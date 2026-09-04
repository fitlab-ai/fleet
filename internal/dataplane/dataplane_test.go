package dataplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/model"
)

type stubPlane struct{ id BackendID }

func (s stubPlane) ID() BackendID { return s.id }
func (stubPlane) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{}, nil
}
func (stubPlane) Validate(context.Context, ValidateRequest) error { return nil }
func (stubPlane) Render(context.Context, RenderRequest) (ConfigArtifact, error) {
	return ConfigArtifact{}, nil
}
func (stubPlane) Start(context.Context, StartRequest) (Instance, error) {
	return Instance{}, nil
}
func (stubPlane) Stop(context.Context, Instance) error { return nil }
func (stubPlane) Probe(context.Context, ProbeRequest) (HealthResult, error) {
	return HealthResult{}, nil
}

func TestRegistrySelectsOnlyConfiguredPlane(t *testing.T) {
	singBox := stubPlane{id: "sing-box"}
	mihomo := stubPlane{id: "mihomo"}
	registry, err := NewRegistry("sing-box", singBox, mihomo)
	if err != nil {
		t.Fatal(err)
	}

	got, err := registry.Configured("")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "sing-box" {
		t.Fatalf("default ID = %q, want sing-box", got.ID())
	}
	got, err = registry.Configured("mihomo")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "mihomo" {
		t.Fatalf("configured ID = %q, want mihomo", got.ID())
	}
}

func TestRegistryRejectsInvalidRegistration(t *testing.T) {
	if _, err := NewRegistry("missing", stubPlane{id: "sing-box"}); err == nil {
		t.Fatal("missing default adapter was accepted")
	}
	if _, err := NewRegistry("sing-box", stubPlane{id: "sing-box"}, stubPlane{id: "sing-box"}); err == nil {
		t.Fatal("duplicate adapter ID was accepted")
	}
}

func TestCapabilitiesRequirePurposeModeProtocolAndExtension(t *testing.T) {
	capabilities := Capabilities{
		Modes:     []Mode{ModeProxy},
		Protocols: []string{"vmess"},
		Extensions: map[string]ExtensionCapability{
			"fleet.test": {Versions: []uint32{1}, MaxBytes: 32},
		},
	}
	node := model.Node{Type: "vmess"}
	extension := &Extension{
		Backend: "sing-box", Schema: "fleet.test", Version: 1,
		Payload: json.RawMessage(`{"enabled":true}`),
	}
	if err := capabilities.Require(ValidateRequest{
		Backend: "sing-box", Purpose: ValidationStart, Mode: ModeProxy,
		Nodes: []model.Node{node}, Extension: extension,
	}); err != nil {
		t.Fatal(err)
	}
	if err := capabilities.Require(ValidateRequest{
		Backend: "sing-box", Purpose: ValidationStart, Mode: ModeTUN,
		Nodes: []model.Node{node},
	}); !IsCode(err, CodeUnsupported) {
		t.Fatalf("mode error = %v, want unsupported", err)
	}
	if err := capabilities.Require(ValidateRequest{
		Backend: "sing-box", Purpose: ValidationRefresh,
		Nodes: []model.Node{{Type: "trojan"}},
	}); !IsCode(err, CodeUnsupported) {
		t.Fatalf("protocol error = %v, want unsupported", err)
	}
	extension.Backend = "mihomo"
	if err := capabilities.Require(ValidateRequest{
		Backend: "sing-box", Purpose: ValidationStart, Mode: ModeProxy,
		Nodes: []model.Node{node}, Extension: extension,
	}); !IsCode(err, CodeUnsupported) {
		t.Fatalf("extension error = %v, want unsupported", err)
	}
}

func TestPublicErrorDoesNotExposeCause(t *testing.T) {
	secret := errors.New("stderr contains token=SECRET")
	err := NewError(CodeConfig, "render", "sing-box", "Configuration is invalid", secret)
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("public error leaked cause: %s", err)
	}
	if !errors.Is(err, secret) {
		t.Fatal("wrapped cause is unavailable to internal diagnostics")
	}
	if !IsCode(err, CodeConfig) {
		t.Fatalf("code not preserved: %v", err)
	}
}
