package app

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestConfigureLauncherKeepsNonRootTUNInSudoSession(t *testing.T) {
	cmd := exec.Command("sudo", "-n", "true")

	configureLauncher(cmd, "tun", 501)

	if cmd.SysProcAttr != nil {
		t.Fatal("non-root TUN launcher must retain the sudo credential session")
	}
}

func TestConfigureLauncherIsolatesProxyProcess(t *testing.T) {
	cmd := exec.Command("sing-box", "run")

	configureLauncher(cmd, "proxy", 501)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("proxy launcher must start in a new session")
	}
}

func TestParseSingBoxPIDUsesFullArgvWhenCommIsTruncated(t *testing.T) {
	output := "  84233   /opt/homebrew/bi   /opt/homebrew/bin/sing-box run -c /tmp/fleet/.config/fleet/sing-box.json -D /tmp/fleet/.config/fleet\n"

	if pid := parseSingBoxPID(output, "/tmp/fleet/.config/fleet"); pid != 84233 {
		t.Fatalf("pid = %d, want 84233", pid)
	}
}

func TestParseSingBoxPIDRequiresFleetConfigArguments(t *testing.T) {
	output := "84233 /opt/homebrew/bi /opt/homebrew/bin/sing-box run -c /tmp/other/sing-box.json -D /tmp/other\n"

	if pid := parseSingBoxPID(output, "/tmp/fleet/.config/fleet"); pid != 0 {
		t.Fatalf("pid = %d, want 0", pid)
	}
}

func TestWaitForReadyReturnsAsSoonAsProcessAndPortAreReady(t *testing.T) {
	calls := 0
	exited := make(chan error, 1)

	pid, err := waitForReady(func() int {
		calls++
		if calls == 4 {
			return 4242
		}
		return 0
	}, func(int) bool {
		return true
	}, exited, 20, 0)

	if err != nil {
		t.Fatal(err)
	}
	if pid != 4242 {
		t.Fatalf("pid = %d, want 4242", pid)
	}
	if calls != 4 {
		t.Fatalf("finder called %d times, want 4", calls)
	}
}

func TestWaitForReadyRejectsLauncherThatExitedImmediately(t *testing.T) {
	exited := make(chan error, 1)
	exited <- errors.New("exit status 1")

	pid, err := waitForReady(func() int { return 99 }, func(int) bool { return true }, exited, 3, time.Nanosecond)

	if err == nil {
		t.Fatal("expected launcher exit error")
	}
	if pid != 0 {
		t.Fatalf("pid = %d, want 0", pid)
	}
}

func TestWaitForReadyRejectsLauncherThatExitedCleanly(t *testing.T) {
	exited := make(chan error, 1)
	exited <- nil

	pid, err := waitForReady(func() int { return 99 }, func(int) bool { return true }, exited, 3, time.Nanosecond)

	if err == nil {
		t.Fatal("expected launcher exit error")
	}
	if pid != 0 {
		t.Fatalf("pid = %d, want 0", pid)
	}
}

func TestWaitForReadyRejectsProcessWithoutListeningPort(t *testing.T) {
	exited := make(chan error, 1)

	pid, err := waitForReady(func() int { return 99 }, func(int) bool { return false }, exited, 3, time.Nanosecond)

	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	if pid != 0 {
		t.Fatalf("pid = %d, want 0", pid)
	}
}

func TestProcessReadyAcceptsTUNWhenRootListenerIsReachable(t *testing.T) {
	ready := processReady("tun", 4242,
		func() int { return 0 },
		func() bool { return true },
	)

	if !ready {
		t.Fatal("TUN process with a strictly matched PID and reachable listener should be ready")
	}
}

func TestProcessReadyStillRequiresMatchingOwnerForProxy(t *testing.T) {
	ready := processReady("proxy", 4242,
		func() int { return 0 },
		func() bool { return true },
	)

	if ready {
		t.Fatal("proxy readiness must require the listener owner PID")
	}
}

func TestTerminateMatchedProcessUsesPrivilegedSignalsAndWaitsForExit(t *testing.T) {
	var signals []syscall.Signal
	findCalls := 0
	err := terminateMatchedProcess(4242, true,
		func(_ int, signal syscall.Signal, privileged bool) error {
			if !privileged {
				t.Fatal("root TUN process must use privileged signals")
			}
			signals = append(signals, signal)
			return nil
		},
		func() int {
			findCalls++
			if findCalls < 3 {
				return 4242
			}
			return 0
		},
		func(time.Duration) {},
	)

	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 || signals[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v, want [SIGTERM]", signals)
	}
}

func TestTerminateMatchedProcessPropagatesPrivilegedSignalFailure(t *testing.T) {
	want := errors.New("sudo denied")
	err := terminateMatchedProcess(4242, true,
		func(_ int, _ syscall.Signal, _ bool) error { return want },
		func() int { return 4242 },
		func(time.Duration) {},
	)

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
