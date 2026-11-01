//go:build !production || server

package main

import "os"

// 开发版与 server 模式默认不检查新版本;设了 MECHBOX_RELEASE_FEED 时读它，格式同 GitHub 的 releases/latest 接口，用来在本地验证。
func releaseFeedURL() string { return os.Getenv("MECHBOX_RELEASE_FEED") }
