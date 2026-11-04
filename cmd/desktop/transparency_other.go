//go:build !darwin || server

package main

func systemReducesTransparency() bool {
	return false
}
