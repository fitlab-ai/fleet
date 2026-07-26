package app

import (
	"context"
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
)

type refreshPlane struct {
	id             dataplane.BackendID
	validations    int
	lastValidation dataplane.ValidateRequest
	probe          func(model.Node) (dataplane.HealthResult, error)
}

func (p *refreshPlane) ID() dataplane.BackendID { return p.id }
func (*refreshPlane) Capabilities(context.Context) (dataplane.Capabilities, error) {
	return dataplane.Capabilities{
		Modes: []dataplane.Mode{dataplane.ModeProxy},
		Protocols: []string{
			"vmess", "hysteria2", "anytls", "trojan",
		},
	}, nil
}
func (p *refreshPlane) Validate(_ context.Context, request dataplane.ValidateRequest) error {
	p.validations++
	p.lastValidation = request
	return nil
}
func (*refreshPlane) Render(context.Context, dataplane.RenderRequest) (dataplane.ConfigArtifact, error) {
	return dataplane.ConfigArtifact{}, nil
}
func (*refreshPlane) Start(context.Context, dataplane.StartRequest) (dataplane.Instance, error) {
	return dataplane.Instance{}, nil
}
func (*refreshPlane) Stop(context.Context, dataplane.Instance) error { return nil }
func (p *refreshPlane) Probe(_ context.Context, request dataplane.ProbeRequest) (dataplane.HealthResult, error) {
	if p.probe != nil && request.Node != nil {
		return p.probe(*request.Node)
	}
	return dataplane.HealthResult{}, nil
}

func testDataPlanes(t *testing.T) *dataplane.Registry {
	t.Helper()
	registry, err := dataplane.NewRegistry("sing-box", &refreshPlane{id: "sing-box"})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestValidateForRefreshUsesOnlyConfiguredDataPlane(t *testing.T) {
	singBox := &refreshPlane{id: "sing-box"}
	mihomo := &refreshPlane{id: "mihomo"}
	registry, err := dataplane.NewRegistry("sing-box", singBox, mihomo)
	if err != nil {
		t.Fatal(err)
	}
	app := App{
		Config:     Config{Backend: "mihomo", Port: 7890},
		DataPlanes: registry,
	}
	if err := app.validateForRefresh(t.Context(), []model.Node{{Type: "vmess"}}); err != nil {
		t.Fatal(err)
	}
	if mihomo.validations != 1 || singBox.validations != 0 {
		t.Fatalf("validations: configured=%d non-configured=%d", mihomo.validations, singBox.validations)
	}
	if mihomo.lastValidation.Purpose != dataplane.ValidationRefresh || mihomo.lastValidation.Mode != "" {
		t.Fatalf("unexpected refresh request: %#v", mihomo.lastValidation)
	}
}

func TestDefaultConfigUsesSingBoxDataPlane(t *testing.T) {
	if got := DefaultConfig().Backend; got != "sing-box" {
		t.Fatalf("default backend = %q", got)
	}
}

func TestExportRequiresDataPlaneRegistry(t *testing.T) {
	app, out := diagnosticApp(t, []model.Node{{Name: "node", Type: "vmess"}})

	if code := app.Export("node", "proxy"); code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	if !strings.Contains(out.String(), "Data plane registry is not configured") {
		t.Fatalf("missing dependency error: %q", out.String())
	}
}

func TestRefreshValidationRequiresDataPlaneRegistry(t *testing.T) {
	app := App{Config: Config{Backend: "sing-box"}}

	err := app.validateForRefresh(t.Context(), []model.Node{{Name: "node", Type: "vmess"}})
	if !dataplane.IsCode(err, dataplane.CodeDependency) {
		t.Fatalf("error=%v, want dependency error", err)
	}
}
