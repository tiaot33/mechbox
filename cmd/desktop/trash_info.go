package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

func trashInfo(original string, deleted time.Time) string {
	segments := strings.Split(filepath.ToSlash(original), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", strings.Join(segments, "/"), deleted.Format("2006-01-02T15:04:05"))
}
