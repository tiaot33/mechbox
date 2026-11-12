//go:build !server

package main

import (
	"os/exec"
	"path/filepath"
)

func reveal(path string) error {
	return exec.Command("xdg-open", filepath.Dir(path)).Run()
}
