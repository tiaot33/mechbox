//go:build !server

package main

import (
	"os/exec"
	"path/filepath"
)

func reveal(path string) error {
	cmd := exec.Command("xdg-open", filepath.Dir(path))
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
