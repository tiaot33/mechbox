package library

import (
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
)

func TestSameFilesIgnoresModesOnlyWhenUnknown(t *testing.T) {
	version := digests([]skillfile.File{
		{Path: "SKILL.md", Content: []byte("doc"), Mode: skillfile.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeExecutable},
	})
	lostMode := digests([]skillfile.File{
		{Path: "SKILL.md", Content: []byte("doc"), Mode: skillfile.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeRegular},
	})
	edited := digests([]skillfile.File{
		{Path: "SKILL.md", Content: []byte("edited"), Mode: skillfile.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeRegular},
	})
	extra := digests([]skillfile.File{
		{Path: "SKILL.md", Content: []byte("doc"), Mode: skillfile.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeRegular},
		{Path: "notes.md", Content: []byte("notes"), Mode: skillfile.ModeRegular},
	})
	tests := []struct {
		name       string
		disk       map[string]skillfile.Digest
		modesKnown bool
		want       bool
	}{
		{"modes known and differ", lostMode, true, false},
		{"modes unknown and only they differ", lostMode, false, true},
		{"modes unknown and content differs", edited, false, false},
		{"modes unknown and a file is added", extra, false, false},
		{"modes known and identical", version, true, true},
	}
	for _, test := range tests {
		if got := sameFiles(test.disk, version, test.modesKnown); got != test.want {
			t.Errorf("%s: sameFiles = %v; want %v", test.name, got, test.want)
		}
	}
}
