package main

import (
	"fmt"
	"strings"
)

func explorerSelect(path string) (string, error) {
	if strings.Contains(path, `"`) {
		return "", fmt.Errorf("reveal in file manager: path contains a double quote: %q", path)
	}
	return `explorer.exe /select,"` + path + `"`, nil
}
