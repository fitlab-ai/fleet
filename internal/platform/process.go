package platform

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fitlab-ai/fleet/internal/dataplane"
)

func FingerprintArgs(args []string) string {
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return hex.EncodeToString(sum[:])
}

type ProcessInspector interface {
	Inspect(pid int) (dataplane.ProcessIdentity, error)
	Signal(dataplane.ProcessIdentity, os.Signal) error
}

type DescendantProcessResolver interface {
	ResolveDescendant(
		context.Context,
		int,
		dataplane.ProcessIdentity,
	) (dataplane.ProcessIdentity, error)
}

type ExecProcessInspector struct{}

func (ExecProcessInspector) Inspect(pid int) (dataplane.ProcessIdentity, error) {
	if pid <= 0 {
		return dataplane.ProcessIdentity{}, fmt.Errorf("invalid pid")
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=,args=").Output()
	if err != nil {
		return dataplane.ProcessIdentity{}, err
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 2 {
		return dataplane.ProcessIdentity{}, fmt.Errorf("process identity unavailable")
	}
	args := fields[1:]
	return dataplane.ProcessIdentity{
		PID: pid, Executable: args[0], ArgsFingerprint: FingerprintArgs(args),
	}, nil
}

func (p ExecProcessInspector) Signal(identity dataplane.ProcessIdentity, signal os.Signal) error {
	current, err := p.Inspect(identity.PID)
	if err != nil {
		return err
	}
	if !SameProcess(current, identity) {
		return dataplane.NewError(
			dataplane.CodeOwnershipUnknown, "signal", "",
			"Process ownership could not be verified", nil,
		)
	}
	process, err := os.FindProcess(identity.PID)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

func SameProcess(current, expected dataplane.ProcessIdentity) bool {
	if current.PID <= 0 || current.PID != expected.PID {
		return false
	}
	return sameProcessSignature(current, expected)
}

func sameProcessSignature(current, expected dataplane.ProcessIdentity) bool {
	if expected.Executable != "" &&
		filepath.Clean(current.Executable) != filepath.Clean(expected.Executable) {
		return false
	}
	return expected.ArgsFingerprint == "" || current.ArgsFingerprint == expected.ArgsFingerprint
}

func (ExecProcessInspector) ResolveDescendant(
	ctx context.Context,
	rootPID int,
	expected dataplane.ProcessIdentity,
) (dataplane.ProcessIdentity, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		output, err := exec.CommandContext(
			resolveCtx, "ps", "-axo", "pid=,ppid=,comm=,args=",
		).Output()
		if err == nil {
			if identity, findErr := FindDescendantProcess(
				string(output), rootPID, expected,
			); findErr == nil {
				return identity, nil
			}
		}
		select {
		case <-resolveCtx.Done():
			return dataplane.ProcessIdentity{}, resolveCtx.Err()
		case <-ticker.C:
		}
	}
}

type processRecord struct {
	parent   int
	identity dataplane.ProcessIdentity
}

func FindDescendantProcess(
	output string,
	rootPID int,
	expected dataplane.ProcessIdentity,
) (dataplane.ProcessIdentity, error) {
	records := make(map[int]processRecord)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil {
			continue
		}
		args := fields[3:]
		records[pid] = processRecord{
			parent: parent,
			identity: dataplane.ProcessIdentity{
				PID: pid, Executable: args[0],
				ArgsFingerprint: FingerprintArgs(args),
			},
		}
	}
	descendants := map[int]bool{rootPID: true}
	for changed := true; changed; {
		changed = false
		for pid, record := range records {
			if !descendants[pid] && descendants[record.parent] {
				descendants[pid] = true
				changed = true
			}
		}
	}
	for pid, record := range records {
		if pid != rootPID && descendants[pid] &&
			sameProcessSignature(record.identity, expected) {
			return record.identity, nil
		}
	}
	return dataplane.ProcessIdentity{}, os.ErrNotExist
}

type LaunchRequest struct {
	Command         []string
	Environment     map[string]string
	Stdout          io.Writer
	Stderr          io.Writer
	NewSession      bool
	NewProcessGroup bool
}

type ProcessHandle interface {
	PID() int
	Wait() error
	Terminate() error
	Kill() error
}

type Launcher interface {
	Start(context.Context, LaunchRequest) (ProcessHandle, error)
}

type Authorizer interface {
	Authorize(context.Context) error
}

type SudoAuthorizer struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (a SudoAuthorizer) Authorize(ctx context.Context) error {
	command := exec.CommandContext(ctx, "sudo", "-v")
	command.Stdin, command.Stdout, command.Stderr = a.Stdin, a.Stdout, a.Stderr
	return command.Run()
}

type ExecLauncher struct{}

func (ExecLauncher) Start(ctx context.Context, request LaunchRequest) (ProcessHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(request.Command) == 0 {
		return nil, fmt.Errorf("launch command is required")
	}
	cmd := exec.Command(request.Command[0], request.Command[1:]...)
	cmd.Env = os.Environ()
	for key, value := range request.Environment {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdout, cmd.Stderr = request.Stdout, request.Stderr
	if request.NewSession {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	} else if request.NewProcessGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return execHandle{cmd: cmd}, nil
}

type execHandle struct{ cmd *exec.Cmd }

func (h execHandle) PID() int    { return h.cmd.Process.Pid }
func (h execHandle) Wait() error { return h.cmd.Wait() }
func (h execHandle) Terminate() error {
	return h.cmd.Process.Signal(syscall.SIGTERM)
}
func (h execHandle) Kill() error { return h.cmd.Process.Kill() }

func ParseProcessList(output string) []dataplane.ProcessIdentity {
	var identities []dataplane.ProcessIdentity
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		args := fields[2:]
		identities = append(identities, dataplane.ProcessIdentity{
			PID: pid, Executable: args[0], ArgsFingerprint: FingerprintArgs(args),
		})
	}
	return identities
}

func WaitForExit(ctx context.Context, inspector ProcessInspector, identity dataplane.ProcessIdentity) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := inspector.Inspect(identity.PID)
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
