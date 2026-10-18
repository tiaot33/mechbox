package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
)

var ErrUnrecognized = errors.New("content is not a SKILL.md, zip, tar or tar.gz")

func Unpack(ctx context.Context, content []byte, maxFiles int, maxBytes int64) (fs.FS, error) {
	switch {
	case bytes.HasPrefix(content, []byte("PK\x03\x04")), bytes.HasPrefix(content, []byte("PK\x05\x06")):
		return readZip(ctx, content, maxFiles, maxBytes)
	case bytes.HasPrefix(content, []byte{0x1f, 0x8b}):
		reader, err := gzip.NewReader(bytes.NewReader(content))
		if err != nil {
			return nil, fmt.Errorf("read source archive: %v: %w", err, source.ErrInvalid)
		}
		expanded, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read source archive: %v: %w", err, source.ErrInvalid)
		}
		if int64(len(expanded)) > maxBytes {
			return nil, fmt.Errorf("read source archive: expanded content exceeds size limit: %w", source.ErrInvalid)
		}
		if !isTar(expanded) {
			return nil, ErrUnrecognized
		}
		return readTar(ctx, expanded, maxFiles, maxBytes)
	case isTar(content):
		return readTar(ctx, content, maxFiles, maxBytes)
	}
	if _, err := discovery.ParseSkillMD(content, "."); err == nil {
		return pack(ctx, []packed{{name: "SKILL.md", mode: 0o644, content: content}}, maxFiles, maxBytes)
	}
	return nil, ErrUnrecognized
}

func isTar(content []byte) bool {
	return len(content) >= 262 && string(content[257:262]) == "ustar"
}

func readZip(ctx context.Context, content []byte, maxFiles int, maxBytes int64) (fs.FS, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("read source archive: %v: %w", err, source.ErrInvalid)
	}
	if len(reader.File) > maxFiles {
		return nil, fmt.Errorf("read source archive: archive exceeds file count limit: %w", source.ErrInvalid)
	}
	seen := make(map[string]bool)
	remaining := uint64(maxBytes)
	for _, archived := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(archived.Name, "/")
		if name == "" || name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) || seen[name] {
			return nil, fmt.Errorf("read source archive: unsafe or duplicate path: %w", source.ErrInvalid)
		}
		seen[name] = true
		if archived.UncompressedSize64 > remaining {
			return nil, fmt.Errorf("read source archive: expanded content exceeds size limit: %w", source.ErrInvalid)
		}
		remaining -= archived.UncompressedSize64
	}
	return reader, nil
}

type packed struct {
	name    string
	mode    fs.FileMode
	content []byte
}

func readTar(ctx context.Context, content []byte, maxFiles int, maxBytes int64) (fs.FS, error) {
	reader := tar.NewReader(bytes.NewReader(content))
	var files []packed
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read source archive: %v: %w", err, source.ErrInvalid)
		}
		switch header.Typeflag {
		case tar.TypeDir, tar.TypeXGlobalHeader:
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("read source archive: unsupported entry type for %q: %w", header.Name, source.ErrInvalid)
		}
		if len(files) == maxFiles {
			return nil, fmt.Errorf("read source archive: archive exceeds file count limit: %w", source.ErrInvalid)
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read source archive: %v: %w", err, source.ErrInvalid)
		}
		files = append(files, packed{name: strings.TrimPrefix(header.Name, "./"), mode: header.FileInfo().Mode(), content: data})
	}
	return pack(ctx, files, maxFiles, maxBytes)
}

func pack(ctx context.Context, files []packed, maxFiles int, maxBytes int64) (fs.FS, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Store}
		header.SetMode(file.mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, fmt.Errorf("read source archive: %v: %w", err, source.ErrInvalid)
		}
		if _, err := entry.Write(file.content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return readZip(ctx, buffer.Bytes(), maxFiles, maxBytes)
}
