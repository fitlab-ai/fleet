package platform

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/fitlab-ai/fleet/internal/dataplane"
)

type ResourceSnapshot map[dataplane.ResourceKind][]string

type ResourceInspector interface {
	PortOwner(context.Context, dataplane.ListenEndpoint) (dataplane.ProcessIdentity, error)
	Snapshot(context.Context, []dataplane.ResourceKind) (ResourceSnapshot, error)
}

type ExecResourceInspector struct{}

func (ExecResourceInspector) PortOwner(ctx context.Context, endpoint dataplane.ListenEndpoint) (dataplane.ProcessIdentity, error) {
	output, err := exec.CommandContext(
		ctx, "lsof", "-nP", fmt.Sprintf("-iTCP:%d", endpoint.Port), "-sTCP:LISTEN", "-Fpca",
	).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return dataplane.ProcessIdentity{}, os.ErrNotExist
		}
		return dataplane.ProcessIdentity{}, err
	}
	return ParseLsofOwner(string(output))
}

func ParseLsofOwner(output string) (dataplane.ProcessIdentity, error) {
	var pid int
	var command string
	var args []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'c':
			command = line[1:]
		case 'a':
			args = append(args, line[1:])
		}
	}
	if pid <= 0 {
		return dataplane.ProcessIdentity{}, fmt.Errorf("port owner unavailable")
	}
	if len(args) == 0 {
		args = []string{command}
	}
	return dataplane.ProcessIdentity{
		PID: pid, Executable: command, ArgsFingerprint: FingerprintArgs(args),
	}, nil
}

func (ExecResourceInspector) Snapshot(ctx context.Context, kinds []dataplane.ResourceKind) (ResourceSnapshot, error) {
	snapshot := ResourceSnapshot{}
	for _, kind := range kinds {
		var command []string
		switch kind {
		case dataplane.ResourceTUN:
			command = []string{"ifconfig", "-l"}
		case dataplane.ResourceRoute:
			command = []string{"netstat", "-rn", "-f", "inet"}
		case dataplane.ResourceDNS:
			command = []string{"scutil", "--dns"}
		default:
			continue
		}
		output, err := exec.CommandContext(ctx, command[0], command[1:]...).Output()
		if err != nil {
			return nil, err
		}
		snapshot[kind] = FingerprintLines(string(output))
	}
	return snapshot, nil
}

func FingerprintLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			sum := sha256.Sum256([]byte(line))
			lines = append(lines, hex.EncodeToString(sum[:]))
		}
	}
	sort.Strings(lines)
	return lines
}
