//go:build !server

package trash

func Discard(path string) error {
	if distro, linux, ok := splitWSLPath(path); ok {
		return trashInDistro(distro, linux)
	}
	return moveToRecycleBin(path)
}
