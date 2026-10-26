package wsl

import (
	"context"
	"os"
	"os/exec"

	"github.com/tiaot33/mechbox/internal/library/internal/process"
)

func shellCommand(distro string) (*exec.Cmd, error) {
	cmd := exec.Command("wsl.exe", "--distribution", distro, "--cd", "~", "--exec", "/bin/sh")
	process.Hide(cmd)
	return cmd, nil
}

func commandKey() string {
	return ""
}

func listDistros(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "wsl.exe", "--list", "--quiet")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	process.Hide(cmd)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	return output, nil
}
