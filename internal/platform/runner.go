package platform

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

type Result struct {
	Code   int
	Stdout string
	Stderr string
}

type Runner interface {
	Run(args []string, stdin string, env map[string]string, timeout time.Duration) (Result, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(args []string, stdin string, env map[string]string, timeout time.Duration) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewBufferString(stdin)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
		err = nil
	}
	return Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}, err
}
