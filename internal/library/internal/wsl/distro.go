package wsl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var ErrNoHome = errors.New("wsl distribution has no home directory")

func Distros(ctx context.Context) ([]string, error) {
	limited, cancel := context.WithTimeout(ctx, homeTimeout)
	defer cancel()
	output, err := listDistros(limited)
	if err != nil && ctx.Err() == nil {
		return nil, nil
	}
	if err != nil || output == nil {
		return nil, err
	}
	var distros []string
	for line := range strings.Lines(decodeOutput(output)) {
		name := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if name == "" || strings.ContainsFunc(name, unicode.IsSpace) || strings.HasPrefix(strings.ToLower(name), "docker-desktop") {
			continue
		}
		distros = append(distros, name)
	}
	return distros, nil
}

func Home(ctx context.Context, distro string) (string, error) {
	output, err := roundTrip(ctx, distro, query(`printf '%s' "$HOME"`, nil), homeTimeout)
	home := string(output)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("%w: %s", ErrNoHome, distro)
	}
	return Join(distro, home), nil
}
