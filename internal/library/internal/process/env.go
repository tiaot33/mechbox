package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

type envKey struct{}

type gitProxyKey struct{}

func WithEnv(ctx context.Context, environ func() []string) context.Context {
	if environ == nil {
		return ctx
	}
	return context.WithValue(ctx, envKey{}, sync.OnceValue(func() []string {
		env := environ()
		if env == nil {
			return []string{}
		}
		return env
	}))
}

func WithGitProxy(ctx context.Context, resolver func(remote string) (string, bool)) context.Context {
	if resolver == nil {
		return ctx
	}
	return context.WithValue(ctx, gitProxyKey{}, resolver)
}

func GitProxy(ctx context.Context, remote string) (string, bool) {
	resolver, ok := ctx.Value(gitProxyKey{}).(func(string) (string, bool))
	if !ok || resolver == nil {
		return "", false
	}
	return resolver(remote)
}

func Getenv(ctx context.Context, key string) string {
	envFn, ok := ctx.Value(envKey{}).(func() []string)
	if !ok {
		return os.Getenv(key)
	}
	return lookup(envFn(), key)
}

func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	envFn, ok := ctx.Value(envKey{}).(func() []string)
	if !ok {
		command := exec.CommandContext(ctx, name, args...)
		command.Env = os.Environ()
		return command
	}
	env := envFn()
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
