package wsl

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func shellCommand(distro string) (*exec.Cmd, error) {
	cmd := exec.Command("wsl.exe", "--distribution", distro, "--cd", "~", "--exec", "/bin/sh")
	hide(cmd)
	return cmd, nil
}

func listDistros(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "wsl.exe", "--list", "--quiet")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	hide(cmd)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	return output, nil
}

func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
