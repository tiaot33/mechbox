package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

type Step struct {
	Version int
	Name    string
	SQL     string
	Go      func(context.Context, *sql.Tx) error
}

//go:embed *
var files embed.FS

var goSteps = map[string]func(context.Context, *sql.Tx) error{}

var fileName = regexp.MustCompile(`^(\d{5})_([a-z0-9]+(?:_[a-z0-9]+)*)\.(sql|go)$`)

func register(up func(context.Context, *sql.Tx) error) {
	_, file, _, _ := runtime.Caller(1)
	goSteps[path.Base(file)] = up
}

func candidate(name string) bool {
	if name == "" || name[0] < '0' || name[0] > '9' || strings.HasSuffix(name, "_test.go") {
		return false
	}
	return strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".go")
}

func FileName(version int, name string) (string, error) {
	file := fmt.Sprintf("%05d_%s.sql", version, name)
	if !fileName.MatchString(file) {
		return "", fmt.Errorf("migration name %q: use lowercase letters, digits and single underscores, like add_skill_pin", name)
	}
	return file, nil
}

func Load(after int) ([]Step, error) {
	return load(files, goSteps, after)
}

func load(fsys fs.FS, registered map[string]func(context.Context, *sql.Tx) error, after int) ([]Step, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var steps []Step
	for _, entry := range entries {
		name := entry.Name()
		if !candidate(name) {
			continue
		}
		match := fileName.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("migration file %s: want NNNNN_name.sql or NNNNN_name.go", name)
		}
		version, _ := strconv.Atoi(match[1])
		step := Step{Version: version, Name: match[2]}
		if match[3] == "sql" {
			data, err := fs.ReadFile(fsys, name)
			if err != nil {
				return nil, err
			}
			step.SQL = string(data)
		} else if step.Go = registered[name]; step.Go == nil {
			return nil, fmt.Errorf("migration file %s does not call register", name)
		}
		steps = append(steps, step)
	}
	slices.SortFunc(steps, func(a, b Step) int { return a.Version - b.Version })
	for i, step := range steps {
		if want := after + 1 + i; step.Version != want {
			return nil, fmt.Errorf("migration %05d_%s: want version %05d, migrations go up by one from %05d", step.Version, step.Name, want, after)
		}
	}
	return steps, nil
}
