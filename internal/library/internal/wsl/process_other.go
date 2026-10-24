//go:build !windows

package wsl

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

const testShell = "MECHBOX_TEST_WSL_SHELL"

func shellCommand(string) (*exec.Cmd, error) {
	if shell := os.Getenv(testShell); shell != "" {
		return exec.Command(shell), nil
	}
	return nil, errors.ErrUnsupported
}

func commandKey() string {
	return os.Getenv(testShell)
}

func listDistros(context.Context) ([]byte, error) {
	if os.Getenv(testShell) != "" {
		return []byte("test\ndocker-desktop\n"), nil
	}
	return nil, nil
}
