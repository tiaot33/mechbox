package skilllock

import (
	"cmp"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
)

const (
	GlobalFile           = ".agents/.skill-lock.json"
	ProjectFile          = "skills-lock.json"
	globalRecordsVersion = 3
)

type Record struct {
	Source       string `json:"source"`
	SourceType   string `json:"sourceType"`
	SourceURL    string `json:"sourceUrl"`
	Ref          string `json:"ref"`
	FolderHash   string `json:"skillFolderHash"`
	ComputedHash string `json:"computedHash"`
}

type Records map[string]Record

func ParseGlobal(content []byte) Records {
	return parse(content, globalRecordsVersion)
}

func ParseProject(content []byte) Records {
	return parse(content, 0)
}

func parse(content []byte, minVersion int) Records {
	var file struct {
		Version *int              `json:"version"`
		Skills  map[string]Record `json:"skills"`
	}
	if json.Unmarshal(content, &file) != nil || file.Version == nil || *file.Version < minVersion {
		return nil
	}
	return file.Skills
}

func (l Records) Find(names ...string) (Record, bool) {
	for _, name := range names {
		if entry, ok := l[name]; ok {
			return entry, true
		}
	}
	return Record{}, false
}

func (e Record) Input() (input, ref string, ok bool) {
	switch e.SourceType {
	case "github":
		input, ref = cmp.Or(e.Source, e.SourceURL), e.Ref
	case "gitlab", "git":
		input, ref = cmp.Or(e.SourceURL, e.Source), e.Ref
	case "well-known", "download":
		input = cmp.Or(e.SourceURL, e.Source)
	}
	return input, ref, input != ""
}

func (e Record) LocalPath() (string, bool) {
	return e.Source, e.SourceType == "local"
}

func (e Record) Matches(files []skillfile.File) bool {
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
