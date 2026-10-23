package wsl

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func localShells(t *testing.T) []string {
	t.Helper()
	var shells []string
	for _, name := range []string{"sh", "dash"} {
		if path, err := exec.LookPath(name); err == nil {
			shells = append(shells, path)
		}
	}
	if len(shells) == 0 {
		t.Skip("no POSIX shell")
	}
	return shells
}

func eachShell(t *testing.T, test func(t *testing.T)) {
	for _, shell := range localShells(t) {
		t.Run(shell, func(t *testing.T) {
			UseCommand(func(string) *exec.Cmd { return exec.Command(shell) })
			test(t)
		})
	}
}

func TestRunReturnsOutputExactly(t *testing.T) {
	eachShell(t, func(t *testing.T) {
		ctx := context.Background()
		args := []string{"it's", "two\nlines", "技能 $HOME `x`", ""}
		output, err := Run(ctx, "test", `for a in "$@"; do printf '%s\0' "$a"; done; printf '\n\n'`, args...)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Join(args, "\x00") + "\x00\n\n"
		if string(output) != want {
			t.Fatalf("output = %q, want %q", output, want)
		}
		big, err := Run(ctx, "test", `i=0; while [ $i -lt 20000 ]; do printf '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n'; i=$((i+1)); done`)
		if err != nil {
			t.Fatal(err)
		}
		if len(big) != 20000*64 || !bytes.HasSuffix(big, []byte("abcde\n")) {
			t.Fatalf("big output has %d bytes", len(big))
		}
	})
}

func TestRunReportsExitStatusAndStderr(t *testing.T) {
	eachShell(t, func(t *testing.T) {
		ctx := context.Background()
		output, err := Run(ctx, "test", `printf partial; echo broken >&2; exit 3`)
		if Status(err) != 3 || string(output) != "partial" {
			t.Fatalf("output %q, err %v", output, err)
		}
		if exit, ok := errors.AsType[*ExitError](err); !ok || strings.TrimSpace(exit.Stderr) != "broken" {
			t.Fatalf("err = %#v", err)
		}
		if output, err := Run(ctx, "test", `printf after`); err != nil || string(output) != "after" {
			t.Fatalf("session not reusable: %q %v", output, err)
		}
	})
}

func TestRunCancelRestartsSession(t *testing.T) {
	eachShell(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := Run(ctx, "test", `sleep 5`); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
		if output, err := Run(context.Background(), "test", `printf again`); err != nil || string(output) != "again" {
			t.Fatalf("after cancel: %q %v", output, err)
		}
	})
}

func TestRunReportsDeadShell(t *testing.T) {
	UseCommand(func(string) *exec.Cmd { return exec.Command("sh", "-c", "echo no such distribution >&2; exit 1") })
	_, err := Run(context.Background(), "gone", `true`)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "no such distribution") {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitAndJoin(t *testing.T) {
	for input, want := range map[string][2]string{
		`\\wsl.localhost\Ubuntu\home\me\.claude`: {"Ubuntu", "/home/me/.claude"},
		`\\WSL$\Ubuntu-24.04\home\me\`:           {"Ubuntu-24.04", "/home/me"},
		`//wsl.localhost/Alpine/root/../tmp`:     {"Alpine", "/tmp"},
		`\\wsl.localhost\Debian`:                 {"Debian", "/"},
	} {
		distro, linux, ok := Split(input)
		if !ok || distro != want[0] || linux != want[1] {
			t.Errorf("Split(%q) = %q %q %v", input, distro, linux, ok)
		}
	}
	for _, input := range []string{`C:\Users\me`, `\\server\share\x`, `\\wsl.localhost\`, "/home/me"} {
		if IsPath(input) {
			t.Errorf("IsPath(%q) = true", input)
		}
	}
	if got := Join("Ubuntu", "/home/me/.claude/skills"); got != `\\wsl.localhost\Ubuntu\home\me\.claude\skills` {
		t.Errorf("Join = %q", got)
	}
	if got := Join("Ubuntu", "/"); got != `\\wsl.localhost\Ubuntu\` {
		t.Errorf("Join root = %q", got)
	}
}

func TestDecodeOutputUTF16(t *testing.T) {
	utf16le := []byte{0xff, 0xfe, 'U', 0, 'b', 0, '\r', 0, '\n', 0, 'A', 0}
	if got := decodeOutput(utf16le); got != "Ub\r\nA" {
		t.Fatalf("decodeOutput = %q", got)
	}
}

func TestQueryReturnsTextExactly(t *testing.T) {
	eachShell(t, func(t *testing.T) {
		ctx := context.Background()
		output, err := Query(ctx, "test", `printf '%s\n\n' "$1"`, "技能 x1 xx")
		if err != nil || output != "技能 x1 xx\n\n" {
			t.Fatalf("output = %q, %v", output, err)
		}
		output, err = Query(ctx, "test", `printf 'x0'; echo 失败 >&2; exit 4`)
		if Status(err) != 4 || output != "x0" {
			t.Fatalf("output %q, err %v", output, err)
		}
		if exit, ok := errors.AsType[*ExitError](err); !ok || strings.TrimSpace(exit.Stderr) != "失败" {
			t.Fatalf("err = %#v", err)
		}
		if output, err := Query(ctx, "test", `true`); err != nil || output != "" {
			t.Fatalf("empty output = %q, %v", output, err)
		}
	})
}

func TestDistrosSkipsMessagesAndDocker(t *testing.T) {
	t.Setenv("MECHBOX_TEST_WSL_SHELL", "sh")
	distros, err := Distros(context.Background())
	if err != nil || len(distros) != 1 || distros[0] != "test" {
		t.Fatalf("Distros = %v, %v", distros, err)
	}
}
