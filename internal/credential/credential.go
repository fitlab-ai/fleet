package credential

import (
	"fmt"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/fitlab-ai/fleet/internal/model"
	"github.com/fitlab-ai/fleet/internal/platform"
)

const Service = "fleet.subscription"

type Backend interface {
	SetURL(subscriptionID, value string) error
	GetURL(subscriptionID string) (string, error)
	DeleteURL(subscriptionID string) error
	IsConfigured(subscriptionID string) bool
}

type Keychain struct {
	Runner   platform.Runner
	Username string
}

func NewKeychain(runner platform.Runner) *Keychain {
	current, _ := user.Current()
	username := ""
	if current != nil {
		username = current.Username
	}
	return &Keychain{Runner: runner, Username: username}
}

func (k *Keychain) Account(subscriptionID string) string {
	if subscriptionID == "" {
		return k.Username
	}
	return k.Username + ":" + subscriptionID
}

func (k *Keychain) run(args []string, input string) (platform.Result, error) {
	if runtime.GOOS != "darwin" {
		return platform.Result{}, model.NewError("credential", "macOS Keychain is unavailable on this platform", nil)
	}
	if k.Runner == nil {
		k.Runner = platform.ExecRunner{}
	}
	return k.Runner.Run(append([]string{"/usr/bin/security"}, args...), input, nil, 15*time.Second)
}

func (k *Keychain) SetURL(id, value string) error {
	result, err := k.run([]string{"add-generic-password", "-U", "-s", Service, "-a", k.Account(id), "-w", value}, "")
	if err != nil || result.Code != 0 {
		return model.NewError("credential", "Keychain operation failed", err)
	}
	return nil
}

func (k *Keychain) GetURL(id string) (string, error) {
	result, err := k.run([]string{"find-generic-password", "-s", Service, "-a", k.Account(id), "-w"}, "")
	if err != nil || result.Code != 0 || strings.TrimSpace(result.Stdout) == "" {
		return "", model.NewError("credential", "Subscription is not configured", err)
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (k *Keychain) DeleteURL(id string) error {
	result, err := k.run([]string{"delete-generic-password", "-s", Service, "-a", k.Account(id)}, "")
	if err != nil || (result.Code != 0 && result.Code != 44) {
		return model.NewError("credential", "Keychain operation failed", fmt.Errorf("exit %d", result.Code))
	}
	return nil
}

func (k *Keychain) IsConfigured(id string) bool {
	value, err := k.GetURL(id)
	return err == nil && value != ""
}
