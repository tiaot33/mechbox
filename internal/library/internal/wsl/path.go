package wsl

import (
	"path"
	"strings"
)

const root = `\\wsl.localhost\`

var prefixes = []string{`\\wsl.localhost\`, `\\wsl$\`, `//wsl.localhost/`, `//wsl$/`}

func Split(input string) (distro, linux string, ok bool) {
	for _, prefix := range prefixes {
		if len(input) < len(prefix) || !strings.EqualFold(input[:len(prefix)], prefix) {
			continue
		}
		rest := strings.ReplaceAll(input[len(prefix):], `\`, "/")
		distro, linux, _ = strings.Cut(rest, "/")
		if distro == "" {
			return "", "", false
		}
		return distro, path.Clean("/" + linux), true
	}
	return "", "", false
}

func IsPath(input string) bool {
	_, _, ok := Split(input)
	return ok
}

func Join(distro, linux string) string {
	cleaned := path.Clean("/" + linux)
	if cleaned == "/" {
		return root + distro + `\`
	}
	return root + distro + strings.ReplaceAll(cleaned, "/", `\`)
}
