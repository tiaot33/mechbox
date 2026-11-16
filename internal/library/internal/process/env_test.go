//go:build unix

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandLooksUpTheGivenPATHOnly(t *testing.T) {
	bin := t.TempDir()
	tool := filepath.Join(bin, "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho \"$GREETING\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := WithEnv(context.Background(), func() []string { return []string{"PATH=/nonexistent", "PATH=" + bin, "GREETING=hello"} })
	output, err := Command(ctx, "tool").Output()
	if err != nil || strings.TrimSpace(string(output)) != "hello" {
		t.Fatalf("tool = %q, %v", output, err)
	}
	if got := Getenv(ctx, "GREETING"); got != "hello" {
		t.Fatalf("Getenv = %q", got)
	}
	// 给定的 PATH 里没有的命令不再到进程的 PATH 里找。
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on the process's PATH")
	}
	if err := Command(ctx, "sh", "-c", "true").Run(); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("sh outside the given PATH = %v; want not found", err)
	}
	if err := Command(context.Background(), "sh", "-c", "true").Run(); err != nil {
		t.Fatalf("sh without a given environment = %v", err)
	}
}

// 给了 environ 函数、它却返回 nil 时当作没有环境变量:找不到命令，也不读进程的环境变量。
func TestNilEnvironmentDoesNotFallBackToTheProcess(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on the process's PATH")
	}
	ctx := WithEnv(context.Background(), func() []string { return nil })
	if err := Command(ctx, "sh", "-c", "true").Run(); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("sh with a nil environment = %v; want not found", err)
	}
	if got := Getenv(ctx, "PATH"); got != "" {
		t.Fatalf("Getenv(PATH) with a nil environment = %q; want empty", got)
	}
	if WithEnv(ctx, nil) != ctx {
		t.Fatal("WithEnv without a function changed the context")
	}
}
