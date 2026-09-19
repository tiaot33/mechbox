package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func filesEqual(first, second []File) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i].Path != second[i].Path || first[i].Mode != second[i].Mode || !bytes.Equal(first[i].Content, second[i].Content) {
			return false
		}
	}
	return true
}

func readFiles(ctx context.Context, root string) ([]File, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("read local source: %w", err)
	}
	if !info.IsDir() {
		return nil, argumentError("read local source", "path", "path is not a directory")
	}

	var files []File
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fetchError(SourceInvalid, "read local source", fmt.Errorf("unsupported file type: %s", path))
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		mode := ModeRegular
		if info.Mode().Perm()&0o111 != 0 {
			mode = ModeExecutable
		}
		files = append(files, File{Path: filepath.ToSlash(relative), Content: content, Mode: mode})
		return nil
	})
	if err != nil {
		if _, ok := errors.AsType[*Error](err); ok {
			return nil, err
		}
		return nil, fmt.Errorf("read local source: %w", err)
	}
	return files, nil
}

// fileMeta 是对照磁盘用的文件指纹:内容 sha256 与可执行位,不含正文。
type fileMeta struct {
	fingerprint string
	mode        FileMode
}

// errIrregularFile 表示遍历遇到符号链接、设备等非常规文件,调用方当作与任何版本都不一致。
var errIrregularFile = errors.New("irregular file")

// hashFiles 遍历 root 下全部常规文件,返回 path → (sha256, mode)。
// 遍历规则与 readFiles 相同(WalkDir、Perm()&0o111);不保留文件内容。
func hashFiles(ctx context.Context, root string) (map[string]fileMeta, error) {
	files := make(map[string]fileMeta)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errIrregularFile
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		mode := ModeRegular
		if info.Mode().Perm()&0o111 != 0 {
			mode = ModeExecutable
		}
		sum := sha256.Sum256(content)
		files[filepath.ToSlash(relative)] = fileMeta{fingerprint: hex.EncodeToString(sum[:]), mode: mode}
		return nil
	})
	if err != nil {
		if errors.Is(err, errIrregularFile) {
			return nil, err
		}
		return nil, fmt.Errorf("hash activation files: %w", err)
	}
	return files, nil
}
