package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type envKey struct{}

func WithEnv(ctx context.Context, environ func() []string) context.Context {
	if environ == nil {
		return ctx
	}
	env := environ()
	if env == nil {
		env = []string{}
	}
	return context.WithValue(ctx, envKey{}, env)
}

func Getenv(ctx context.Context, key string) string {
	env, ok := ctx.Value(envKey{}).([]string)
	if !ok {
		return os.Getenv(key)
	}
	return lookup(env, key)
}

func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	env, ok := ctx.Value(envKey{}).([]string)
	if !ok {
		command := exec.CommandContext(ctx, name, args...)
		command.Env = os.Environ()
		return command
	}
	path, found := lookPath(env, name)
	command := exec.CommandContext(ctx, path, args...)
	command.Env = slices.Clone(env)
	if !found {
		command.Err = &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return command
}

func lookup(env []string, key string) string {
	for _, pair := range slices.Backward(env) {
		name, value, ok := strings.Cut(pair, "=")
		if ok && (name == key || runtime.GOOS == "windows" && strings.EqualFold(name, key)) {
			return value
		}
	}
	return ""
}

func lookPath(env []string, name string) (string, bool) {
	if strings.ContainsAny(name, `/\`) {
		return name, true
	}
	for _, dir := range filepath.SplitList(lookup(env, "PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if found, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return found, true
		}
	}
	return name, false
}
