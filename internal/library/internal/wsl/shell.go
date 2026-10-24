package wsl

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

var (
	ErrUnavailable = errors.New("wsl distribution unavailable")
	ErrTooLarge    = errors.New("wsl response too large")
)

const (
	missingStatus  = 3
	requestTimeout = 2 * time.Minute
	homeTimeout    = 30 * time.Second
	maxResponse    = 256 << 20
)

type ExitError struct {
	Status int
	Stderr string
}

func (e *ExitError) Error() string {
	message := "wsl command exited with status " + strconv.Itoa(e.Status)
	if stderr := strings.TrimSpace(e.Stderr); stderr != "" {
		message += ": " + stderr
	}
	return message
}

func (e *ExitError) Is(target error) bool {
	return target == fs.ErrNotExist && e.Status == missingStatus
}

func Status(err error) int {
	if exit, ok := errors.AsType[*ExitError](err); ok {
		return exit.Status
	}
	return -1
}

const marker = "@@mechbox-response"

const prelude = `export LC_ALL=C
umask 022
T=$(mktemp -d) || exit 1
trap 'rm -rf "$T"' EXIT
`

type session struct {
	distro string
	lock   chan struct{}
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *limitedBuffer
	exited chan struct{}
}

var (
	sessionsMu sync.Mutex
	sessions   = map[string]*session{}
)

func sessionFor(distro string) *session {
	key := strings.ToLower(distro) + "\x00" + commandKey()
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	s := sessions[key]
	if s == nil {
		s = &session{distro: distro, lock: make(chan struct{}, 1)}
		sessions[key] = s
	}
	return s
}

func Run(ctx context.Context, distro, script string, args ...string) ([]byte, error) {
	return roundTrip(ctx, distro, request(script, args), requestTimeout)
}

func Query(ctx context.Context, distro, script string, args ...string) (string, error) {
	output, err := roundTrip(ctx, distro, query(script, args), requestTimeout)
	return string(output), err
}

func roundTrip(ctx context.Context, distro, req string, timeout time.Duration) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := sessionFor(distro)
	limited, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case s.lock <- struct{}{}:
	case <-limited.Done():
		return nil, s.abandoned(ctx)
	}
	defer func() { <-s.lock }()
	if err := s.start(); err != nil {
		return nil, err
	}
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, err := s.exchange(req)
		done <- result{output, err}
	}()
	select {
	case r := <-done:
		if r.err != nil && !errors.As(r.err, new(*ExitError)) {
			s.stop()
		}
		return r.output, r.err
	case <-limited.Done():
		s.stop()
		<-done
		return nil, s.abandoned(ctx)
	}
}

func (s *session) abandoned(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s: no response", ErrUnavailable, s.distro)
}

func (s *session) start() error {
	if s.cmd != nil {
		select {
		case <-s.exited:
			s.stop()
		default:
			return nil
		}
	}
	cmd, err := shellCommand(s.distro)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, s.distro, err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, s.distro, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, s.distro, err)
	}
	stderr := &limitedBuffer{limit: 4096}
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, s.distro, err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	s.cmd, s.stdin, s.stdout, s.stderr, s.exited = cmd, stdin, bufio.NewReaderSize(stdout, 64<<10), stderr, exited
	if _, err := io.WriteString(stdin, prelude); err != nil {
		return s.failure(err)
	}
	return nil
}

func (s *session) stop() {
	if s.cmd == nil {
		return
	}
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	<-s.exited
	s.cmd, s.stdin, s.stdout = nil, nil, nil
}

func (s *session) failure(err error) error {
	if s.exited != nil {
		select {
		case <-s.exited:
		case <-time.After(2 * time.Second):
		}
	}
	if text := decodeOutput(s.stderr.Bytes()); strings.TrimSpace(text) != "" {
		return fmt.Errorf("%w: %s: %s", ErrUnavailable, s.distro, strings.TrimSpace(text))
	}
	return fmt.Errorf("%w: %s: %w", ErrUnavailable, s.distro, err)
}

func (s *session) exchange(req string) ([]byte, error) {
	stdin, stdout := s.stdin, s.stdout
	if _, err := io.WriteString(stdin, req); err != nil {
		return nil, s.failure(err)
	}
	var fields []string
	for {
		line, err := stdout.ReadString('\n')
		if err != nil {
			return nil, s.failure(err)
		}
		if rest, ok := strings.CutPrefix(line, marker+" "); ok {
			fields = strings.Fields(rest)
			break
		}
	}
	if len(fields) != 3 {
		return nil, s.failure(fmt.Errorf("malformed response header %q", fields))
	}
	var numbers [3]int
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return nil, s.failure(fmt.Errorf("malformed response header %q", fields))
		}
		numbers[i] = n
	}
	if numbers[1]+numbers[2] > maxResponse {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, numbers[1]+numbers[2])
	}
	body := make([]byte, numbers[1]+numbers[2])
	if _, err := io.ReadFull(stdout, body); err != nil {
		return nil, s.failure(err)
	}
	output, stderr := body[:numbers[1]], body[numbers[1]:]
	if numbers[0] != 0 {
		return output, &ExitError{Status: numbers[0], Stderr: string(stderr)}
	}
	return output, nil
}

func request(script string, args []string) string {
	var b strings.Builder
	writeArgs(&b, args)
	b.WriteString("(\n")
	b.WriteString(script)
	b.WriteString("\n) </dev/null >\"$T/o\" 2>\"$T/e\"\n")
	b.WriteString("s=$?\nset -- $(wc -c <\"$T/o\") $(wc -c <\"$T/e\")\n")
	b.WriteString("printf '" + marker + " %s %s %s\\n' \"$s\" \"$1\" \"$2\"\n")
	b.WriteString("cat \"$T/o\" \"$T/e\"\n")
	return b.String()
}

func query(script string, args []string) string {
	var b strings.Builder
	writeArgs(&b, args)
	b.WriteString("o=$( (\n")
	b.WriteString(script)
	b.WriteString("\n) </dev/null 2>\"$T/e\"; printf x%s \"$?\" )\n")
	b.WriteString("s=${o##*x}\no=${o%x*}\n")
	b.WriteString("if [ \"$s\" = 0 ]; then printf '" + marker + " 0 %s 0\\n%s' \"${#o}\" \"$o\"\n")
	b.WriteString("else set -- $(wc -c <\"$T/e\"); printf '" + marker + " %s %s %s\\n%s' \"$s\" \"${#o}\" \"$1\" \"$o\"; cat \"$T/e\"; fi\n")
	return b.String()
}

func writeArgs(b *strings.Builder, args []string) {
	b.WriteString("set --")
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(quote(arg))
	}
	b.WriteByte('\n')
}

func quote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func decodeOutput(data []byte) string {
	if len(data) >= 2 && bytes.IndexByte(data, 0) >= 0 {
		units := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
		}
		return strings.TrimPrefix(string(utf16.Decode(units)), "\ufeff")
	}
	return string(data)
}

type limitedBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - len(b.data); room > 0 {
		b.data = append(b.data, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.data)
}
