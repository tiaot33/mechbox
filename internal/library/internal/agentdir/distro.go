package agentdir

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

type distro struct {
	name string
}

const (
	lstatScript = `if [ -L "$1" ]; then printf l; elif [ -d "$1" ]; then printf d; elif [ -e "$1" ]; then printf o; else exit 3; fi`
	statScript  = `if [ -d "$1" ]; then printf d; elif [ -e "$1" ]; then printf o; else exit 3; fi`
	evalScript  = `[ -e "$1" ] || exit 3
readlink -f -- "$1"`
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
	canonicalScript = `for c in "$@"; do
	rest=
	while :; do
		if [ -e "$c" ] && r=$(readlink -f -- "$c"); then
			break
		fi
		p=$(dirname -- "$c")
		if [ "$p" = "$c" ]; then
			r=$c
			break
		fi
		rest=$(basename -- "$c")${rest:+/$rest}
		c=$p
	done
	printf '%s\0%s\0' "$r" "$rest"
done`
	inspectScript = `n=$1
b=$2
c=$3
shift 3
u=0
key() {
	d=$(dirname -- "$1")
	k=$(readlink -f -- "$d" 2>/dev/null) && [ -n "$k" ] || k=$d
	printf '%s\0%s\0' "$k" "$(basename -- "$1")"
}
one() {
	if [ -L "$1" ]; then t=l; elif [ -d "$1" ]; then t=d; elif [ -e "$1" ]; then t=o; else t=-; fi
	r=
	if [ "$t" = l ] && [ -e "$1" ]; then r=$(readlink -f -- "$1") || r=; fi
	printf '%s\0%s\0' "$t" "$r"
	key "$1"
	h=$1
	i=0
	while [ "$i" -lt 40 ] && [ -L "$h" ] && l=$(readlink -- "$h"); do
		case $l in
		/*) h=$l ;;
		*) h=$(dirname -- "$h")/$l ;;
		esac
		printf '%s\0' "$h"
		key "$h"
		i=$((i + 1))
	done
	printf '\0'
	[ "$n" = - ] && return
	if [ ! -e "$1" ] || ! d=$(readlink -f -- "$1") || [ ! -d "$d" ]; then
		printf 'irregular\0'
		return
	fi
	if ! (cd -- "$d" && find . PRUNE -prune -o ! -type d -print) >"$T/l" 2>"$T/m"; then
		printf 'error\0%s\0' "$(cat "$T/m")"
		return
	fi
	f=0
	s=0
	while IFS= read -r e; do
		if [ -L "$d/$e" ] || [ ! -f "$d/$e" ]; then
			printf 'irregular\0'
			return
		fi
		f=$((f + 1))
		s=$((s + $(wc -c <"$d/$e")))
		if [ "$f" -gt "$n" ] || [ "$s" -gt "$b" ]; then
			printf 'large\0'
			return
		fi
	done <"$T/l"
	if [ "$u" -gt 0 ] && [ $((u + s)) -gt "$c" ]; then
		printf 'deferred\0'
		return
	fi
	u=$((u + s))
	if [ "$f" -eq 0 ]; then
		printf 'ok\0%s\0' 0
		return
	fi
	if ! (cd -- "$d" && tar -cf - -T "$T/l") >"$T/a" 2>"$T/m"; then
		printf 'error\0%s\0' "$(cat "$T/m")"
		return
	fi
	printf 'ok\0%s\0' "$(($(wc -c <"$T/a")))"
	cat "$T/a"
}
for p in "$@"; do
	one "$p"
done`
	listScript = `cd -- "$1" 2>/dev/null || exit 0
shift
for e in *; do
	[ -d "$e" ] && [ -f "$e/SKILL.md" ] || continue
	t=
	if [ -L "$e" ]; then t=$(readlink -f -- "$e") || continue; fi
	printf '%s\0%s\0' "$e" "$t"
	[ -r "$e/SKILL.md" ] || continue
	s=$(($(wc -c <"$e/SKILL.md")))
	[ "$s" -le 1048576 ] && set -- "$@" "./$e/SKILL.md"
done
printf '\0'
[ $# -eq 0 ] || tar -chf - "$@" 2>/dev/null || :`
	committedScript = `GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 exec git -c core.fsmonitor=false -C "$1" ls-tree -r -z --name-only HEAD -- "$2"`
	mkdirTempScript = `mkdir -p -- "$1" && mktemp -d "$1/$2XXXXXX"`
	copyScript      = `cp -RPp -- "$1/." "$2"`
	renameScript    = `[ -e "$1" ] || [ -L "$1" ] || exit 3
if [ -e "$2" ] || [ -L "$2" ]; then echo "target exists" >&2; exit 4; fi
mv -- "$1" "$2"`
	removeScript = `[ -e "$1" ] || [ -L "$1" ] || exit 0
[ -L "$1" ] || find "$1" -type d ! -perm -700 -exec chmod u+rwx {} + 2>/dev/null
rm -rf -- "$1"`
	readBatchBytes = 64 << 20
)

var inspectWithPrune = strings.Replace(inspectScript, "PRUNE", prunePattern(), 1)

func prunePattern() string {
	var names []string
	for _, name := range skillfile.IgnoredPatterns() {
		names = append(names, "-name '"+name+"'")
	}
	return `\( ` + strings.Join(names, " -o ") + ` \)`
}

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
	return output, d.failure(op, p, err)
}

func (d distro) query(ctx context.Context, op, p, script string, args ...string) (string, error) {
	output, err := wsl.Query(ctx, d.name, script, args...)
	return output, d.failure(op, p, err)
}

func (d distro) failure(op, p string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return &fs.PathError{Op: op, Path: p, Err: fs.ErrNotExist}
	case errors.Is(err, wsl.ErrTooLarge):
		return fmt.Errorf("%s %s: %w: %w", op, p, ErrTooLarge, err)
	}
	return fmt.Errorf("%s %s: %w", op, p, err)
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
	kind, err := d.query(context.Background(), op, p, script, d.linux(p))
	if err != nil {
		return entry{}, err
	}
	return entry{dir: kind == "d", link: kind == "l"}, nil
}

func (d distro) evalSymlinks(p string) (string, error) {
	output, err := d.query(context.Background(), "lstat", p, evalScript, d.linux(p))
	if err != nil {
		return "", err
	}
	return d.path(strings.TrimSuffix(output, "\n")), nil
}

func (d distro) readlink(p string) (string, error) {
	output, err := d.query(context.Background(), "readlink", p, `readlink -- "$1"`, d.linux(p))
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(output, "\n"), nil
}

func (d distro) readFile(p string) ([]byte, error) {
	output, err := wsl.ReadFile(context.Background(), d.name, d.linux(p))
	return output, d.failure("open", p, err)
}

func (d distro) sameEntry(a, b string) (bool, bool) {
	return d.test(sameEntryScript, d.linux(a), d.linux(b))
}

func (d distro) sameFolder(a, b string) (bool, bool) {
	return d.test(sameFolderScript, d.linux(a), d.linux(b))
}

func (d distro) within(directories []string) func(string) bool {
	canonical := d.canonicalAll(directories)
	return func(resolved string) bool {
		return slices.ContainsFunc(canonical, func(directory string) bool {
			_, inside := d.relative(directory, resolved)
			return inside
		})
	}
}

func (d distro) canonical(p string) string {
	return d.canonicalAll([]string{p})[0]
}

func (d distro) canonicalAll(paths []string) []string {
	canonical := make([]string, len(paths))
	args := make([]string, len(paths))
	for i, p := range paths {
		canonical[i] = d.clean(p)
		args[i] = d.linux(p)
	}
	output, err := d.run(context.Background(), "canonical", paths[0], canonicalScript, args...)
	if err != nil {
		return canonical
	}
	fields := strings.Split(string(output), "\x00")
	for i := range paths {
		if 2*i+1 < len(fields) {
			canonical[i] = d.path(path.Join(fields[2*i], fields[2*i+1]))
		}
	}
	return canonical
}

func (d distro) inspect(ctx context.Context, folders []string, read *limits, visit func(int, FolderState) error) error {
	pending := make([]int, len(folders))
	for i := range folders {
		pending[i] = i
	}
	for len(pending) > 0 {
		args := []string{"-", "0", "0"}
		if read != nil {
			args = []string{strconv.Itoa(read.files), strconv.FormatInt(read.bytes, 10), strconv.Itoa(readBatchBytes)}
		}
		for _, index := range pending {
			args = append(args, d.linux(folders[index]))
		}
		output, err := d.run(ctx, "inspect", folders[pending[0]], inspectWithPrune, args...)
		if err != nil {
			return err
		}
		parser := &inspection{d: d, rest: output}
		var deferred []int
		for _, index := range pending {
			state, again, err := parser.next(folders[index], read != nil)
			if err != nil {
				return fmt.Errorf("inspect %s: %w", folders[index], err)
			}
			if again {
				deferred = append(deferred, index)
				continue
			}
			if err := visit(index, state); err != nil {
				return err
			}
		}
		if len(deferred) == len(pending) {
			return fmt.Errorf("inspect %s: no progress", folders[pending[0]])
		}
		pending = deferred
	}
	return nil
}

type inspection struct {
	d    distro
	rest []byte
}

var errMalformed = errors.New("malformed response")

func (p *inspection) field() (string, error) {
	field, rest, ok := bytes.Cut(p.rest, []byte{0})
	if !ok {
		return "", errMalformed
	}
	p.rest = rest
	return string(field), nil
}

func (p *inspection) key() (string, error) {
	directory, err := p.field()
	if err != nil {
		return "", err
	}
	base, err := p.field()
	if err != nil {
		return "", err
	}
	return p.d.path(path.Join(directory, base)), nil
}

func (p *inspection) next(folder string, read bool) (FolderState, bool, error) {
	state := FolderState{Path: folder}
	kind, err := p.field()
	if err != nil {
		return state, false, err
	}
	resolved, err := p.field()
	if err != nil {
		return state, false, err
	}
	if state.key, err = p.key(); err != nil {
		return state, false, err
	}
	for {
		hopPath, err := p.field()
		if err != nil {
			return state, false, err
		}
		if hopPath == "" {
			break
		}
		hopKey, err := p.key()
		if err != nil {
			return state, false, err
		}
		state.chain = append(state.chain, hop{path: p.d.path(hopPath), key: hopKey})
	}
	state.Exists = kind != "-"
	state.Resolves = state.Exists && (kind != "l" || resolved != "")
	if kind == "l" && resolved != "" {
		state.LinkTarget = p.d.path(resolved)
	} else if kind == "l" && len(state.chain) > 0 {
		state.LinkTarget = state.chain[0].path
	}
	if !read {
		return state, false, nil
	}
	status, err := p.field()
	if err != nil {
		return state, false, err
	}
	switch status {
	case "irregular":
		state.Err = ErrIrregularFile
	case "large":
		state.Err = ErrTooLarge
	case "deferred":
		return state, true, nil
	case "error":
		message, err := p.field()
		if err != nil {
			return state, false, err
		}
		state.Err = fmt.Errorf("read installed files: %s", strings.TrimSpace(message))
	case "ok":
		size, err := p.field()
		if err != nil {
			return state, false, err
		}
		n, err := strconv.Atoi(size)
		if err != nil || n < 0 || n > len(p.rest) {
			return state, false, errMalformed
		}
		archive := p.rest[:n]
		p.rest = p.rest[n:]
		state.Files, state.Modified, state.Err = filesOf(archive)
	default:
		return state, false, errMalformed
	}
	return state, false, nil
}

func filesOf(archive []byte) ([]skillfile.File, time.Time, error) {
	var files []skillfile.File
	var modified time.Time
	err := readArchive(archive, func(header *tar.Header, name string, content []byte) error {
		if header.Typeflag == tar.TypeDir || skillfile.Ignored(name) {
			return nil
		}
		if content == nil {
			return ErrIrregularFile
		}
		files = append(files, skillfile.File{Path: name, Content: content, Mode: skillfile.ModeOf(fs.FileMode(header.Mode).Perm())})
		if header.ModTime.After(modified) {
			modified = header.ModTime
		}
		return nil
	})
	if errors.Is(err, ErrIrregularFile) {
		return nil, time.Time{}, err
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("read installed files: %w", err)
	}
	slices.SortFunc(files, func(a, b skillfile.File) int { return cmp.Compare(a.Path, b.Path) })
	return files, modified, nil
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
	parser := &inspection{d: d, rest: output}
	var folders []Folder
	for {
		name, err := parser.field()
		if err != nil {
			return nil, fmt.Errorf("list skill folders: %w", err)
		}
		if name == "" {
			break
		}
		target, err := parser.field()
		if err != nil {
			return nil, fmt.Errorf("list skill folders: %w", err)
		}
		folder := Folder{Name: name, Path: d.join(directory, name)}
		if target != "" {
			folder.Target = d.path(target)
		}
		folders = append(folders, folder)
	}
	manifests := map[string][]byte{}
	_ = readArchive(parser.rest, func(_ *tar.Header, name string, content []byte) error {
		if folder, file, ok := strings.Cut(name, "/"); ok && file == "SKILL.md" && content != nil {
			manifests[folder] = content
		}
		return nil
	})
	for i := range folders {
		folders[i].SkillMD = manifests[folders[i].Name]
	}
	return folders, nil
}

func (d distro) committed(ctx context.Context, root, directory string) (map[string]bool, error) {
	output, err := d.run(ctx, "committed", root, committedScript, d.linux(root), directory)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	return committedNames(output, directory), nil
}

func (d distro) mkdirTemp(directory, prefix string) (string, error) {
	output, err := d.query(context.Background(), "stage", directory, mkdirTempScript, d.linux(directory), prefix)
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
	if _, err := d.run(context.Background(), "write", stage, extractScript(archive.Bytes()), d.linux(stage)); err != nil {
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
	if _, err := d.query(ctx, "copy", from, copyScript, d.linux(from), d.linux(stage)); err != nil {
		return fmt.Errorf("copy skill folder: %w", err)
	}
	return nil
}

func (d distro) rename(from, to string) error {
	_, err := d.query(context.Background(), "rename", from, renameScript, d.linux(from), d.linux(to))
	return err
}

func (d distro) removeTree(p string) error {
	_, err := d.query(context.Background(), "remove", p, removeScript, d.linux(p))
	return err
}
