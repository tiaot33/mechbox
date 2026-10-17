//go:build !server

package main

import "os/exec"

func reveal(path string) error {
	return exec.Command("open", "-R", path).Run()
}
