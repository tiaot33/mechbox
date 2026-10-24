package agentdir

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

var (
	ErrNotAbsolute = errors.New("path is not absolute")
	ErrNotRelative = errors.New("path is not relative")
	ErrOutside     = errors.New("path is outside the root")
)

type Home struct {
	root string
}

func UserHome() (Home, error) {
	root, err := os.UserHomeDir()
	if err != nil {
		return Home{}, fmt.Errorf("find home directory: %w", err)
	}
	return Home{root: root}, nil
}

func DistroHome(ctx context.Context, distro string) (Home, error) {
	root, err := wsl.Home(ctx, distro)
	if err != nil {
		return Home{}, fmt.Errorf("find home directory in %s: %w", distro, err)
	}
	return Home{root: root}, nil
}

func (h Home) Root() string {
	return h.root
}

func (h Home) Join(relative string) string {
	return Join(h.root, relative)
}

func (h Home) Relative(input string) (string, error) {
	path := input
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = Join(h.root, strings.TrimPrefix(path[1:], "/"))
	}
	if !on(path).isAbs(path) {
		return "", ErrNotAbsolute
	}
	relative, inside := relativeInside(h.root, path)
	if !inside {
		relative, inside = relativeInside(Canonical(h.root), Canonical(path))
	}
	if !inside {
		return "", ErrOutside
	}
	return relative, nil
}

func ProjectRelative(input string) (string, error) {
	if filepath.IsAbs(input) || wsl.IsPath(input) {
		return "", ErrNotRelative
	}
	relative := filepath.Clean(input)
	if relative == "." || !filepath.IsLocal(relative) {
		return "", ErrOutside
	}
	return filepath.ToSlash(relative), nil
}

func relativeInside(root, path string) (string, bool) {
	if !sameSystem(root, path) {
		return "", false
	}
	return on(root).relative(root, path)
}
