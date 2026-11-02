package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

var stepName = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)

func main() {
	if len(os.Args) != 2 || !stepName.MatchString(os.Args[1]) {
		fmt.Fprintln(os.Stderr, "用法: go run ./internal/library/internal/store/migrationgen <这一步做了什么，小写字母、数字和下划线，如 add_skill_pin>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(name string) error {
	dir, err := storeDir()
	if err != nil {
		return err
	}
	baselines, err := filepath.Glob(filepath.Join(dir, "testdata", "[0-9][0-9][0-9][0-9][0-9]_schema.sql"))
	if err != nil {
		return err
	}
	if len(baselines) != 1 {
		return fmt.Errorf("%s 下应正好有一份 NNNNN_schema.sql 起点结构，找到 %d 份", filepath.Join(dir, "testdata"), len(baselines))
	}
	baseline, err := os.ReadFile(baselines[0])
	if err != nil {
		return err
	}
	schema, err := os.ReadFile(filepath.Join(dir, "schema.sql"))
	if err != nil {
		return err
	}
	plan, err := store.PlanMigration(context.Background(), string(baseline), string(schema))
	if err != nil {
		return err
	}
	if plan.SQL == "" {
		fmt.Println("schema.sql 与跑完已有升级后的结构一致，不用新增升级")
		return nil
	}
	file := filepath.Join(dir, "migrations", fmt.Sprintf("%05d_%s.sql", plan.Version, name))
	if err := os.WriteFile(file, []byte(plan.SQL), 0o644); err != nil {
		return err
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintln(os.Stderr, "注意:", warning)
	}
	fmt.Printf("已生成 %s,看过后和 schema.sql 一起提交\n", file)
	return nil
}

func storeDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "internal", "library", "internal", "store"), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("在仓库里运行：找不到 go.mod")
		}
		dir = parent
	}
}
