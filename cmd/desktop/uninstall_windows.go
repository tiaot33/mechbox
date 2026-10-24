//go:build !server

package main

func removeFromSystem() error { return nil }

func discard(path string) error {
	if distro, linux, ok := splitWSLPath(path); ok {
		return trashInDistro(distro, linux)
	}
	return trash(path)
}
