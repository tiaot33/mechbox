//go:build !darwin || server

package main

import "os"

func removeFromSystem() error { return nil }

func discard(path string) error { return os.RemoveAll(path) }
