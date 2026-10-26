//go:build !server

package main

import "golang.org/x/sys/windows"

// systemLanguage 是 Windows 设置里排第一的显示语言。Windows 一般不设 LANG 等环境变量。
func systemLanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
