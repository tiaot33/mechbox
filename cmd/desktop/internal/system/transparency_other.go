//go:build !darwin || server

package system

func ReducesTransparency() bool {
	return false
}
