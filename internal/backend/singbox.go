package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
)

var versionRE = regexp.MustCompile(`\b([0-9]+)\.([0-9]+)\.([0-9]+)([-+][^\s]+)?\b`)

type SingBox struct {
	Binary string
	Runner platform.Runner
}

func (s SingBox) CheckVersion() ([3]int, error) {
	var zero [3]int
	runner := s.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	result, err := runner.Run([]string{s.Binary, "version"}, "", nil, 10*time.Second)
	match := versionRE.FindStringSubmatch(result.Stdout)
	message := "sing-box >= 1.13.14 is required; upgrade with: brew upgrade sing-box"
	if err != nil || result.Code != 0 || match == nil {
		return zero, model.NewError("sing-box", message, err)
	}
	version := [3]int{}
	for i := range 3 {
		version[i], _ = strconv.Atoi(match[i+1])
	}
	if match[4] != "" && match[4][0] == '-' || version[0] < 1 ||
		(version[0] == 1 && version[1] < 13) ||
		(version[0] == 1 && version[1] == 13 && version[2] < 14) {
		return zero, model.NewError("sing-box", fmt.Sprintf("%s (detected %d.%d.%d)", message, version[0], version[1], version[2]), nil)
	}
	return version, nil
}

func (s SingBox) ValidateNodes(nodes []model.Node, port int) error {
	if _, err := os.Stat(s.Binary); err != nil {
		return model.NewError("sing-box", "sing-box is not installed", err)
	}
	if _, err := s.CheckVersion(); err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "fleet-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	runner := s.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	for i, node := range nodes {
		config, err := BuildProxyConfig(node, port)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(config)
		path := filepath.Join(root, fmt.Sprintf("node-%d.json", i))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		result, runErr := runner.Run([]string{s.Binary, "check", "-c", path}, "", nil, 30*time.Second)
		if runErr != nil || result.Code != 0 {
			return model.NewError("sing-box", fmt.Sprintf("Node '%s' failed sing-box validation", node.Name), runErr)
		}
	}
	return nil
}
