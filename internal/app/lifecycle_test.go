package app

import (
	"testing"
	"time"
)

func TestWaitForPIDReturnsAsSoonAsProcessIsFound(t *testing.T) {
	calls := 0
	fallbackCalled := false

	pid := waitForPID(func() int {
		calls++
		if calls == 4 {
			return 4242
		}
		return 0
	}, func() int {
		fallbackCalled = true
		return 99
	}, 20, 0)

	if pid != 4242 {
		t.Fatalf("pid = %d, want 4242", pid)
	}
	if calls != 4 {
		t.Fatalf("finder called %d times, want 4", calls)
	}
	if fallbackCalled {
		t.Fatal("fallback called after PID was found")
	}
}

func TestWaitForPIDUsesFallbackAfterPollingExpires(t *testing.T) {
	calls := 0

	pid := waitForPID(func() int {
		calls++
		return 0
	}, func() int {
		return 99
	}, 3, time.Nanosecond)

	if pid != 99 {
		t.Fatalf("pid = %d, want fallback PID 99", pid)
	}
	if calls != 3 {
		t.Fatalf("finder called %d times, want 3", calls)
	}
}
