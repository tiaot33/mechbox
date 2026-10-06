package skillfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"slices"
)

// Mode 记录文件是否可执行,安装到磁盘时据此设置权限位。
type Mode string

const (
	ModeRegular    Mode = "regular"
	ModeExecutable Mode = "executable"
)

// File 是 skill 里的一个文件;Path 是 skill 内斜杠分隔的相对路径。
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

func ModeOf(mode fs.FileMode) Mode {
	if mode.Perm()&0o111 != 0 {
		return ModeExecutable
	}
	return ModeRegular
}
