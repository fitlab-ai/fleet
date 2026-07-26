package app

import (
	"context"
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
	"github.com/fitlab-ai/fleet/internal/model"
)

type refreshPlane struct {
	id          dataplane.BackendID
	validations int
}

func (p *refreshPlane) ID() dataplane.BackendID { return p.id }
func (*refreshPlane) Capabilities(context.Context) (dataplane.Capabilities, error) {
	return dataplane.Capabilities{
		Modes: []dataplane.Mode{dataplane.ModeProxy},
		Protocols: []string{
			"vmess",
		},
	}, nil
}
func (p *refreshPlane) Validate(_ context.Context, request dataplane.ValidateRequest) error {
	p.validations++
	if request.Purpose != dataplane.ValidationRefresh || request.Mode != "" {
		return dataplane.NewError(
			dataplane.CodeInvalidRequest, "refresh", p.id,
			"unexpected refresh request", nil,
		)
	}
	return nil
}
func (*refreshPlane) Render(context.Context, dataplane.RenderRequest) (dataplane.ConfigArtifact, error) {
	return dataplane.ConfigArtifact{}, nil
}
func (*refreshPlane) Start(context.Context, dataplane.StartRequest) (dataplane.Instance, error) {
	return dataplane.Instance{}, nil
}
func (*refreshPlane) Stop(context.Context, dataplane.Instance) error { return nil }
func (*refreshPlane) Probe(context.Context, dataplane.ProbeRequest) (dataplane.HealthResult, error) {
	return dataplane.HealthResult{}, nil
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
}

func TestDefaultConfigUsesSingBoxDataPlane(t *testing.T) {
	if got := DefaultConfig().Backend; got != "sing-box" {
		t.Fatalf("default backend = %q", got)
	}
}
