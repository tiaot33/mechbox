//go:build server

package trash

import "os"

func Discard(path string) error { return os.RemoveAll(path) }
