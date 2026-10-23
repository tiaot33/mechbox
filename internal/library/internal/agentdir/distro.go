package agentdir

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

type distro struct {
	name string
}

const missingStatus = 3

const (
	lstatScript = `if [ -L "$1" ]; then printf l; elif [ -d "$1" ]; then printf d; elif [ -f "$1" ]; then printf f; elif [ -e "$1" ]; then printf o; else exit 3; fi`
	statScript  = `if [ -d "$1" ]; then printf d; elif [ -f "$1" ]; then printf f; elif [ -e "$1" ]; then printf o; else exit 3; fi`
	evalScript  = `[ -e "$1" ] || exit 3
readlink -f -- "$1"`
	readScript = `[ -e "$1" ] || exit 3
cat -- "$1"`
	sameEntryScript = `{ [ -e "$1" ] || [ -L "$1" ]; } && { [ -e "$2" ] || [ -L "$2" ]; } || exit 3
if [ -L "$1" ] || [ -L "$2" ]; then
	a=$(readlink -f -- "$(dirname -- "$1")") || exit 3
	b=$(readlink -f -- "$(dirname -- "$2")") || exit 3
	[ "$a/$(basename -- "$1")" = "$b/$(basename -- "$2")" ]
else
	[ "$1" -ef "$2" ]
fi`
	sameFolderScript = `[ -e "$1" ] && [ -e "$2" ] || exit 3
[ "$1" -ef "$2" ]`
	withinScript = `[ -e "$2" ] || exit 3
if [ -e "$1" ] && p=$(readlink -f -- "$1"); then c=$p; else c=$1; fi
while :; do
	n=$(dirname -- "$c")
	[ "$n" = "$c" ] && exit 1
	c=$n
	[ "$c" -ef "$2" ] && exit 0
done`
	canonicalScript = `c=$1
rest=
while :; do
	if [ -e "$c" ] && r=$(readlink -f -- "$c"); then
		printf '%s\n%s' "$r" "$rest"
		exit 0
	fi
	p=$(dirname -- "$c")
	if [ "$p" = "$c" ]; then
		printf '%s\n%s' "$c" "$rest"
		exit 0
	fi
	rest=$(basename -- "$c")${rest:+/$rest}
	c=$p
done`
	walkScript = `[ -e "$1" ] || exit 3
r=$(readlink -f -- "$1") || exit 3
[ -d "$r" ] || exit 3
cd -- "$r" || exit 1
tar -cf - .`
	listScript = `cd -- "$1" 2>/dev/null || exit 0
shift
for e in *; do
	[ -d "$e" ] && [ -f "$e/SKILL.md" ] || continue
	t=
	if [ -L "$e" ]; then t=$(readlink -f -- "$e") || continue; fi
	s=$(($(wc -c <"$e/SKILL.md")))
	printf '%s\0%s\0' "$e" "$t"
	[ "$s" -le 1048576 ] && set -- "$@" "./$e/SKILL.md"
done
printf '\0'
[ $# -eq 0 ] || tar -chf - "$@"`
	committedScript = `GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 exec git -c core.fsmonitor=false -C "$1" ls-tree -r -z --name-only HEAD -- "$2"`
	mkdirTempScript = `mkdir -p -- "$1" || exit 3
mktemp -d "$1/$2XXXXXX"`
	copyScript   = `cp -RPp -- "$1/." "$2"`
	renameScript = `[ -e "$1" ] || [ -L "$1" ] || exit 3
if [ -e "$2" ] || [ -L "$2" ]; then echo "target exists" >&2; exit 4; fi
mv -- "$1" "$2"`
	removeScript = `[ -e "$1" ] || [ -L "$1" ] || exit 0
[ -L "$1" ] || find "$1" -type d ! -perm -700 -exec chmod u+rwx {} + 2>/dev/null
rm -rf -- "$1"`
)

func (d distro) key() string { return "wsl:" + strings.ToLower(d.name) }

func (d distro) linux(p string) string {
	_, linux, ok := wsl.Split(p)
	if !ok {
		return p
	}
	return linux
}

func (d distro) path(linux string) string { return wsl.Join(d.name, linux) }

func (d distro) run(ctx context.Context, op, p, script string, args ...string) ([]byte, error) {
	output, err := wsl.Run(ctx, d.name, script, args...)
	if wsl.Status(err) == missingStatus {
		return output, &fs.PathError{Op: op, Path: p, Err: fs.ErrNotExist}
	}
	return output, err
}

func (d distro) query(op, p, script string, args ...string) (string, error) {
	output, err := wsl.Query(context.Background(), d.name, script, args...)
	if wsl.Status(err) == missingStatus {
		return output, &fs.PathError{Op: op, Path: p, Err: fs.ErrNotExist}
	}
	return output, err
}

func (d distro) test(script string, args ...string) (bool, bool) {
	_, err := wsl.Query(context.Background(), d.name, script, args...)
	if err == nil {
		return true, true
	}
	return false, wsl.Status(err) == 1
}

func (d distro) join(root, relative string) string {
	return d.path(path.Join(d.linux(root), relative))
}

func (d distro) dir(p string) string { return d.path(path.Dir(d.linux(p))) }

func (d distro) base(p string) string { return path.Base(d.linux(p)) }

func (d distro) clean(p string) string { return d.path(d.linux(p)) }

func (distro) isAbs(string) bool { return true }

func (d distro) abs(p string) (string, error) { return d.clean(p), nil }

func (d distro) relative(root, p string) (string, bool) {
	root, p = d.linux(root), d.linux(p)
	if root == "/" {
		rest := strings.TrimPrefix(p, "/")
		return rest, rest != ""
	}
	rest, ok := strings.CutPrefix(p, root+"/")
	return rest, ok && rest != ""
}

func (d distro) resolveLink(p, link string) string {
	if !strings.HasPrefix(link, "/") {
		link = path.Join(path.Dir(d.linux(p)), link)
	}
	return d.path(link)
}

func (d distro) lstat(p string) (entry, error) {
	return d.entry("lstat", p, lstatScript)
}

func (d distro) stat(p string) (entry, error) {
	return d.entry("stat", p, statScript)
}

func (d distro) entry(op, p, script string) (entry, error) {
	kind, err := d.query(op, p, script, d.linux(p))
	if err != nil {
		return entry{}, err
	}
	return entry{dir: kind == "d", link: kind == "l", regular: kind == "f"}, nil
}

func (d distro) evalSymlinks(p string) (string, error) {
	output, err := d.query("lstat", p, evalScript, d.linux(p))
	if err != nil {
		return "", err
	}
	return d.path(strings.TrimSuffix(output, "\n")), nil
}

func (d distro) readlink(p string) (string, error) {
	output, err := d.query("readlink", p, `readlink -- "$1"`, d.linux(p))
	if err != nil {
		return "", &fs.PathError{Op: "readlink", Path: p, Err: err}
	}
	return strings.TrimSuffix(output, "\n"), nil
}

func (d distro) readFile(p string) ([]byte, error) {
	return d.run(context.Background(), "open", p, readScript, d.linux(p))
}

func (d distro) sameEntry(a, b string) (bool, bool) {
	return d.test(sameEntryScript, d.linux(a), d.linux(b))
}

func (d distro) sameFolder(a, b string) (bool, bool) {
	return d.test(sameFolderScript, d.linux(a), d.linux(b))
}

func (d distro) within(p, directory string) (bool, bool) {
	return d.test(withinScript, d.linux(p), d.linux(directory))
}

func (d distro) canonical(p string) string {
	output, err := wsl.Query(context.Background(), d.name, canonicalScript, d.linux(p))
	if err != nil {
		return d.clean(p)
	}
	resolved, rest, _ := strings.Cut(output, "\n")
	return d.path(path.Join(resolved, rest))
}

func (d distro) walk(ctx context.Context, root string, visit func(walked) error) error {
	output, err := d.run(ctx, "walk", root, walkScript, d.linux(root))
	if errors.Is(err, fs.ErrNotExist) {
		return ErrIrregularFile
	}
	if err != nil {
		return fmt.Errorf("hash installed files: %w", err)
	}
	err = readArchive(output, func(header *tar.Header, name string, content []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if skillfile.Ignored(name) || header.Typeflag == tar.TypeDir {
			return nil
		}
		if content == nil {
			return ErrIrregularFile
		}
		return visit(walked{
			relative: name,
			mode:     fs.FileMode(header.Mode).Perm(),
			size:     int64(len(content)),
			modTime:  header.ModTime,
			read:     func() ([]byte, error) { return content, nil },
		})
	})
	if err != nil {
		if errors.Is(err, ErrIrregularFile) || errors.Is(err, ErrTooLarge) || errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("hash installed files: %w", err)
	}
	return nil
}

func readArchive(archive []byte, visit func(header *tar.Header, name string, content []byte) error) error {
	reader := tar.NewReader(bytes.NewReader(archive))
	regular := map[string][]byte{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean("/"+header.Name), "/")
		if name == "" {
			continue
		}
		var content []byte
		switch header.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeReg, '\x00':
			if content, err = io.ReadAll(reader); err != nil {
				return err
			}
			if content == nil {
				content = []byte{}
			}
			regular[name] = content
		case tar.TypeLink:
			content = regular[strings.TrimPrefix(path.Clean("/"+header.Linkname), "/")]
		}
		if err := visit(header, name, content); err != nil {
			return err
		}
	}
}

func (d distro) list(directory string) ([]Folder, error) {
	output, err := d.run(context.Background(), "list", directory, listScript, d.linux(directory))
	if err != nil {
		return nil, fmt.Errorf("list skill folders: %w", err)
	}
	if len(output) == 0 {
		return nil, nil
	}
	var folders []Folder
	rest := output
	for {
		name, after, ok := bytes.Cut(rest, []byte{0})
		if !ok {
			return nil, fmt.Errorf("list skill folders: malformed listing")
		}
		rest = after
		if len(name) == 0 {
			break
		}
		target, after, ok := bytes.Cut(rest, []byte{0})
		if !ok {
			return nil, fmt.Errorf("list skill folders: malformed listing")
		}
		rest = after
		folder := Folder{Name: string(name), Path: d.join(directory, string(name))}
		if len(target) > 0 {
			folder.Target = d.path(string(target))
		}
		folders = append(folders, folder)
	}
	manifests := map[string][]byte{}
	err = readArchive(rest, func(_ *tar.Header, name string, content []byte) error {
		if folder, file, ok := strings.Cut(name, "/"); ok && file == "SKILL.md" && content != nil {
			manifests[folder] = content
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list skill folders: %w", err)
	}
	for i := range folders {
		folders[i].SkillMD = manifests[folders[i].Name]
	}
	return folders, nil
}

func (d distro) committed(ctx context.Context, root, directory string) (map[string]bool, error) {
	output, err := wsl.Run(ctx, d.name, committedScript, d.linux(root), directory)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	return committedNames(output, directory), nil
}

func (d distro) mkdirTemp(directory, prefix string) (string, error) {
	output, err := wsl.Query(context.Background(), d.name, mkdirTempScript, d.linux(directory), prefix)
	if wsl.Status(err) == missingStatus {
		return "", fmt.Errorf("create scope: %w", err)
	}
	if err != nil {
		return "", fmt.Errorf("stage skill folder: %w", err)
	}
	return d.path(strings.TrimSuffix(output, "\n")), nil
}

func (d distro) writeFiles(stage string, files []skillfile.File) error {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	now := time.Now()
	for _, file := range files {
		if !filepath.IsLocal(file.Path) {
			return fmt.Errorf("invalid skill path: %s", file.Path)
		}
		mode := int64(0o644)
		if file.Mode == skillfile.ModeExecutable {
			mode = 0o755
		}
		header := &tar.Header{Typeflag: tar.TypeReg, Name: file.Path, Mode: mode, Size: int64(len(file.Content)), ModTime: now, Format: tar.FormatPAX}
		if err := writer.WriteHeader(header); err != nil {
			return fmt.Errorf("write skill file: %w", err)
		}
		if _, err := writer.Write(file.Content); err != nil {
			return fmt.Errorf("write skill file: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}
	if _, err := wsl.Run(context.Background(), d.name, extractScript(archive.Bytes()), d.linux(stage)); err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}
	return nil
}

func extractScript(archive []byte) string {
	const delimiter = "@@MECHBOX_ARCHIVE@@"
	encoded := base64.StdEncoding.EncodeToString(archive)
	var script strings.Builder
	script.WriteString("base64 -d <<'" + delimiter + "' | tar -xf - -C \"$1\"\n")
	for len(encoded) > 76 {
		script.WriteString(encoded[:76])
		script.WriteByte('\n')
		encoded = encoded[76:]
	}
	script.WriteString(encoded)
	script.WriteString("\n" + delimiter)
	return script.String()
}

func (d distro) copyTree(ctx context.Context, from, stage string) error {
	if _, err := wsl.Query(ctx, d.name, copyScript, d.linux(from), d.linux(stage)); err != nil {
		return fmt.Errorf("copy skill folder: %w", err)
	}
	return nil
}

func (d distro) rename(from, to string) error {
	_, err := d.query("rename", from, renameScript, d.linux(from), d.linux(to))
	return err
}

func (d distro) removeTree(p string) error {
	if _, err := wsl.Query(context.Background(), d.name, removeScript, d.linux(p)); err != nil {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}
