package skillfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

type Mode string

const (
	ModeRegular    Mode = "regular"
	ModeExecutable Mode = "executable"
)

type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Mode    Mode   `json:"mode"`
}

type Digest struct {
	Fingerprint string
	Mode        Mode
}

var ignoredNames = []string{".git", ".DS_Store", "__MACOSX", "__pycache__", "__pypackages__", "Thumbs.db", "desktop.ini"}

func IgnoredPatterns() []string {
	return append(slices.Clone(ignoredNames), "._*")
}

func Ignored(path string) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if slices.Contains(ignoredNames, segment) || strings.HasPrefix(segment, "._") {
			return true
		}
	}
	return false
}

func Equal(first, second []File) bool {
	return slices.EqualFunc(first, second, func(a, b File) bool {
		return a.Path == b.Path && a.Mode == b.Mode && bytes.Equal(a.Content, b.Content)
	})
}

func Digests(files []File) map[string]Digest {
	result := make(map[string]Digest, len(files))
	for _, file := range files {
		result[file.Path] = Digest{Fingerprint: Fingerprint(file.Content), Mode: file.Mode}
	}
	return result
}

func SameContent(a, b map[string]Digest) bool {
	return maps.EqualFunc(a, b, func(x, y Digest) bool { return x.Fingerprint == y.Fingerprint })
}

func WithModesOf(files []File, reference map[string]Digest) []File {
	adopted := slices.Clone(files)
	for i, file := range adopted {
		if digest, ok := reference[file.Path]; ok {
			adopted[i].Mode = digest.Mode
		}
	}
	return adopted
}

func SameModes(files, others []File) bool {
	modes := make(map[string]Mode, len(others))
	for _, file := range others {
		modes[file.Path] = file.Mode
	}
	for _, file := range files {
		if mode, ok := modes[file.Path]; ok && mode != file.Mode {
			return false
		}
	}
	return true
}

func Fingerprint(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func Sum(files []File) string {
	sorted := slices.SortedFunc(slices.Values(files), func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	hash := sha256.New()
	for _, file := range sorted {
		fmt.Fprintf(hash, "%s\x00%s\x00%s\n", file.Path, file.Mode, Fingerprint(file.Content))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func ContentSum(files []File) string {
	sorted := slices.SortedFunc(slices.Values(files), func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	hash := sha256.New()
	for _, file := range sorted {
		fmt.Fprintf(hash, "%s\x00%s\n", file.Path, Fingerprint(file.Content))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func ModeOf(mode fs.FileMode) Mode {
	if mode.Perm()&0o111 != 0 {
		return ModeExecutable
	}
	return ModeRegular
}

func Similarity(old, latest []File) float64 {
	byPath := make(map[string]File, len(latest))
	for _, file := range latest {
		byPath[file.Path] = file
	}
	var matched float64
	for _, file := range old {
		other, ok := byPath[file.Path]
		if !ok {
			continue
		}
		a, b := file.Content, other.Content
		if file.Path == "SKILL.md" {
			a, b = withoutName(a), withoutName(b)
		}
		if bytes.Equal(a, b) {
			matched++
			continue
		}
		matched += lineOverlap(a, b)
	}
	return matched / float64(max(len(old), len(latest)))
}

func withoutName(content []byte) []byte {
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		return content
	}
	frontmatter, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return content
	}
	var kept [][]byte
	for line := range bytes.SplitSeq(frontmatter, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("name:")) {
			kept = append(kept, line)
		}
	}
	return append(append(bytes.Join(kept, []byte("\n")), '\n'), body...)
}

func lineOverlap(a, b []byte) float64 {
	counts := map[string]int{}
	var total, common int
	for line := range strings.SplitSeq(string(a), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			counts[line]++
			total++
		}
	}
	lines := 0
	for line := range strings.SplitSeq(string(b), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		lines++
		if counts[line] > 0 {
			counts[line]--
			common++
		}
	}
	if total == 0 || lines == 0 {
		return 0
	}
	return float64(common) / float64(max(total, lines))
}
