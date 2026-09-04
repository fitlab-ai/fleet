package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestLifecycleCommandsRequireRuntimeManager(t *testing.T) {
	tests := []struct {
		name string
		run  func(*App) int
	}{
		{name: "start", run: func(app *App) int { return app.Start("node", "proxy") }},
		{name: "stop", run: func(app *App) int { return app.Stop() }},
		{name: "switch", run: func(app *App) int { return app.Switch("node", "proxy") }},
		{name: "status", run: func(app *App) int { return app.Status() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			app := &App{Out: &out}

			if code := test.run(app); code != 1 {
				t.Fatalf("code = %d, want 1; output=%q", code, out.String())
			}
			if !strings.Contains(out.String(), "Runtime manager is not configured") {
				t.Fatalf("missing dependency error: %q", out.String())
			}
		})
	}
}
