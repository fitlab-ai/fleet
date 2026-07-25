package backend

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
)

type singBoxRunner struct {
	result   platform.Result
	err      error
	check    platform.Result
	checkErr error
}

func (r singBoxRunner) Run(command []string, _ string, _ map[string]string, _ time.Duration) (platform.Result, error) {
	if len(command) > 1 && command[1] == "check" {
		return r.check, r.checkErr
	}
	return r.result, r.err
}

func TestSingBoxVersionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		output string
		ok     bool
	}{
		{"sing-box version 1.13.14", true},
		{"sing-box version 1.14.0", true},
		{"sing-box version 1.13.13", false},
		{"sing-box version 1.14.0-beta.1", false},
		{"not a version", false},
	} {
		_, err := (SingBox{Runner: singBoxRunner{result: platform.Result{Stdout: tc.output}}}).CheckVersion()
		if (err == nil) != tc.ok {
			t.Errorf("output=%q err=%v, want ok=%v", tc.output, err, tc.ok)
		}
	}
}

func TestSingBoxValidationIdentifiesNodeSafely(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	node := model.Node{Name: "safe-name", Type: "vmess", Server: "example.com", Port: 443, UUID: "secret-uuid"}
	runner := singBoxRunner{result: platform.Result{Stdout: "sing-box version 1.13.14"}}
	box := SingBox{Binary: binary, Runner: runner}
	if err := box.ValidateNodes([]model.Node{node}, 7890); err != nil {
		t.Fatal(err)
	}
	box.Runner = singBoxRunner{
		result: platform.Result{Stdout: "sing-box version 1.13.14"},
		check:  platform.Result{Code: 1}, checkErr: errors.New("check failed"),
	}
	err = box.ValidateNodes([]model.Node{node}, 7890)
	if err == nil || !strings.Contains(err.Error(), "safe-name") || strings.Contains(err.Error(), "secret-uuid") {
		t.Fatalf("unsafe or missing node error: %v", err)
	}
}
