//go:build !windows

package wsl

import (
	"context"
	"errors"
	"os/exec"
)

func shellCommand(string) (*exec.Cmd, error) {
	if shell := emulatedShell(); shell != "" {
		return exec.Command(shell), nil
	}
	return nil, errors.ErrUnsupported
}

func commandKey() string {
	return emulatedShell()
}

func listDistros(context.Context) ([]byte, error) {
	if emulatedShell() != "" {
		return []byte("test\ndocker-desktop\n"), nil
	}
	return nil, nil
}
