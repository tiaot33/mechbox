//go:build !server

package system

import "os/exec"

func Reveal(path string) error {
	return exec.Command("open", "-R", path).Run()
}
