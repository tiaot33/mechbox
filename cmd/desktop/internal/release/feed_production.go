//go:build production && !server

package release

func FeedURL() string { return releaseFeed }
