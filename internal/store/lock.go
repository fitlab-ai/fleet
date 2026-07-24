package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/fitlab-ai/fleet/internal/model"
)

type PIDLock struct {
	Path string
	Held bool
}

func (l *PIDLock) Acquire() error {
	if data, err := os.ReadFile(l.Path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if process, err := os.FindProcess(pid); err == nil && process.Signal(syscall.Signal(0)) == nil {
				return model.NewError("lock", "Another Fleet write operation is running", nil)
			}
		}
		_ = os.Remove(l.Path)
	}
	if err := SecureDir(filepath.Dir(l.Path)); err != nil {
		return err
	}
	if err := os.WriteFile(l.Path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	l.Held = true
	return nil
}

func (l *PIDLock) Release() {
	if l.Held {
		_ = os.Remove(l.Path)
		l.Held = false
	}
}

type CompositeLock struct{ Refresh, Writer PIDLock }

func NewCompositeLock(root string) *CompositeLock {
	return &CompositeLock{
		Refresh: PIDLock{Path: filepath.Join(root, "refresh.lock")},
		Writer:  PIDLock{Path: filepath.Join(root, "writer.lock")},
	}
}

func (l *CompositeLock) Acquire() error {
	if err := l.Refresh.Acquire(); err != nil {
		return err
	}
	if err := l.Writer.Acquire(); err != nil {
		l.Refresh.Release()
		return err
	}
	return nil
}
func (l *CompositeLock) Release() { l.Writer.Release(); l.Refresh.Release() }
