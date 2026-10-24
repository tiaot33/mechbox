package main

import (
	"path"
	"strings"
)

const distroTrashScript = `p=$1
[ -e "$p" ] || [ -L "$p" ] || exit 0
d=${XDG_DATA_HOME:-$HOME/.local/share}/Trash
mkdir -p "$d/files" "$d/info" || exit 1
b=$(basename -- "$p")
n=$b
i=0
while :; do
	if [ ! -e "$d/files/$n" ] && [ ! -L "$d/files/$n" ] && (set -C; printf '%s' "$2" >"$d/info/$n.trashinfo") 2>/dev/null; then
		break
	fi
	i=$((i+1))
	[ "$i" -lt 10000 ] || exit 1
	n=$b.$i
done
mv -- "$p" "$d/files/$n" || { rm -f "$d/info/$n.trashinfo"; exit 1; }`

func splitWSLPath(input string) (distro, linux string, ok bool) {
	for _, prefix := range []string{`\\wsl.localhost\`, `\\wsl$\`, `//wsl.localhost/`, `//wsl$/`} {
		if len(input) < len(prefix) || !strings.EqualFold(input[:len(prefix)], prefix) {
			continue
		}
		distro, rest, _ := strings.Cut(strings.ReplaceAll(input[len(prefix):], `\`, "/"), "/")
		if distro == "" {
			return "", "", false
		}
		return distro, path.Clean("/" + rest), true
	}
	return "", "", false
}
