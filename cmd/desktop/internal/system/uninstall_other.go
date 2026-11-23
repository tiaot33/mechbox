//go:build !darwin || server

package system

func RemoveFromSystem(string) error { return nil }
