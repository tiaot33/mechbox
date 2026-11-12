//go:build server || !(darwin || windows || linux)

package main

func reveal(string) error { return nil }
