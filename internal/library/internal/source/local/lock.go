package local

import (
	"cmp"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

const (
	globalLockVersion = 3
	projectLockFile   = "skills-lock.json"
)

type LockEntry struct {
	Source       string `json:"source"`
	SourceType   string `json:"sourceType"`
	SourceURL    string `json:"sourceUrl"`
	Ref          string `json:"ref"`
	FolderHash   string `json:"skillFolderHash"`
	ComputedHash string `json:"computedHash"`
	root         string
}

type Lockfile map[string]LockEntry

func GlobalLockfile(home string) Lockfile {
	path := filepath.Join(home, ".agents", ".skill-lock.json")
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		path = filepath.Join(state, "skills", ".skill-lock.json")
	}
	return readLockfile(path, "", globalLockVersion)
}

func ProjectLockfile(root string) Lockfile {
	return readLockfile(filepath.Join(root, projectLockFile), root, 0)
}

func readLockfile(path, root string, minVersion int) Lockfile {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var file struct {
		Version *int                 `json:"version"`
		Skills  map[string]LockEntry `json:"skills"`
	}
	if json.Unmarshal(content, &file) != nil || file.Version == nil || *file.Version < minVersion {
		return nil
	}
	lock := Lockfile{}
	for name, entry := range file.Skills {
		entry.root = root
		lock[name] = entry
	}
	return lock
}

func (l Lockfile) Find(names ...string) (LockEntry, bool) {
	for _, name := range names {
		if entry, ok := l[name]; ok {
			return entry, true
		}
	}
	return LockEntry{}, false
}

func (e LockEntry) Input() (input, ref string, ok bool) {
	switch e.SourceType {
	case "github":
		input, ref = cmp.Or(e.Source, e.SourceURL), e.Ref
	case "gitlab", "git":
		input, ref = cmp.Or(e.SourceURL, e.Source), e.Ref
	case "well-known", "download":
		input = cmp.Or(e.SourceURL, e.Source)
	case "local":
		switch {
		case filepath.IsAbs(e.Source):
			input = e.Source
		case e.Source != "" && e.root != "":
			input = filepath.Join(e.root, filepath.FromSlash(e.Source))
		}
	}
	return input, ref, input != ""
}

func (e LockEntry) Matches(files []skillfile.File) bool {
	want := strings.ToLower(cmp.Or(e.ComputedHash, e.FolderHash))
	return want != "" && (want == contentHash(files) || want == hex.EncodeToString(treeHash(files)))
}

func contentHash(files []skillfile.File) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b skillfile.File) int { return strings.Compare(a.Path, b.Path) })
	hash := sha256.New()
	for _, file := range sorted {
		hash.Write([]byte(file.Path))
		hash.Write(file.Content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func treeHash(files []skillfile.File) []byte {
	type entry struct {
		name, mode string
		sum        []byte
	}
	var entries []entry
	nested := map[string][]skillfile.File{}
	for _, file := range files {
		if dir, rest, ok := strings.Cut(file.Path, "/"); ok {
			nested[dir] = append(nested[dir], skillfile.File{Path: rest, Content: file.Content, Mode: file.Mode})
			continue
		}
		mode := "100644"
		if file.Mode == skillfile.ModeExecutable {
			mode = "100755"
		}
		entries = append(entries, entry{file.Path, mode, object("blob", file.Content)})
	}
	for dir, children := range nested {
		entries = append(entries, entry{dir, "40000", treeHash(children)})
	}
	key := func(e entry) string {
		if e.mode == "40000" {
			return e.name + "/"
		}
		return e.name
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(key(a), key(b)) })
	var body []byte
	for _, e := range entries {
		body = fmt.Appendf(body, "%s %s\x00", e.mode, e.name)
		body = append(body, e.sum...)
	}
	return object("tree", body)
}

func object(kind string, content []byte) []byte {
	hash := sha1.New()
	fmt.Fprintf(hash, "%s %d\x00", kind, len(content))
	hash.Write(content)
	return hash.Sum(nil)
}
