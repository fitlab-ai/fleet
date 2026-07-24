package app

import (
	"errors"
	"testing"
	"time"
)

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
