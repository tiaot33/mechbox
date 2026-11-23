//go:build !server

package trash

func Discard(path string) error { return moveToFreedesktopTrash(path) }
