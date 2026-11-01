//go:build production && !server

package main

func releaseFeedURL() string { return releaseFeed }
