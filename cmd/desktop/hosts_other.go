//go:build !server

package main

import "github.com/tiaot33/mechbox/internal/library"

func openDesktopLibrary(path string) (*library.Library, error) {
	return library.Open(path)
}
