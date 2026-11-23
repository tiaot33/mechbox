//go:build production && !server

package release

func feedURL() string { return releaseFeed }
