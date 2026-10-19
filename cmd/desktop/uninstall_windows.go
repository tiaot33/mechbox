//go:build !server

package main

func removeFromSystem() error { return nil }

func discard(path string) error { return trash(path) }
