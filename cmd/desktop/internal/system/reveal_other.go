//go:build server || !(darwin || windows || linux)

package system

func Reveal(string) error { return nil }
