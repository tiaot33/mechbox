package skillfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
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

func Equal(first, second []File) bool {
	return slices.EqualFunc(first, second, func(a, b File) bool {
		return a.Path == b.Path && a.Mode == b.Mode && bytes.Equal(a.Content, b.Content)
	})
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

func ModeOf(mode fs.FileMode) Mode {
	if mode.Perm()&0o111 != 0 {
		return ModeExecutable
	}
	return ModeRegular
}
