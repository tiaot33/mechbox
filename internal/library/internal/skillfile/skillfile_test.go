package skillfile

import (
	"slices"
	"testing"
)

func TestSimilarity(t *testing.T) {
	skill := func(content string) File {
		return File{Path: "SKILL.md", Content: []byte(content), Mode: ModeRegular}
	}
	file := func(path, content string) File {
		return File{Path: path, Content: []byte(content), Mode: ModeRegular}
	}
	tests := []struct {
		name        string
		old, latest []File
		want        float64
	}{
		{
			name:   "same content",
			old:    []File{skill("---\nname: pdf\n---\nbody\n"), file("notes.md", "a\nb\n")},
			latest: []File{skill("---\nname: pdf\n---\nbody\n"), file("notes.md", "a\nb\n")},
			want:   1,
		},
		{
			name:   "only frontmatter name differs",
			old:    []File{skill("---\nname: pdf\ndescription: read pdf\n---\nbody\n")},
			latest: []File{skill("---\nname: pdf-tools\ndescription: read pdf\n---\nbody\n")},
			want:   1,
		},
		{
			name:   "name line outside frontmatter counts",
			old:    []File{skill("name: pdf\nbody\n")},
			latest: []File{skill("name: pdf-tools\nbody\n")},
			want:   0.5,
		},
		{
			name:   "name line in other files counts",
			old:    []File{file("notes.md", "---\nname: pdf\n---\nbody\n")},
			latest: []File{file("notes.md", "---\nname: pdf-tools\n---\nbody\n")},
			want:   0.75,
		},
		{
			name:   "completely different",
			old:    []File{skill("---\nname: pdf\n---\nread pdf\n")},
			latest: []File{skill("---\nname: csv\n---\nwrite csv\n")},
			want:   0,
		},
		{
			name:   "no common lines",
			old:    []File{file("notes.md", "a\nb\n")},
			latest: []File{file("notes.md", "c\nd\n")},
			want:   0,
		},
		{
			name:   "some lines in common",
			old:    []File{file("notes.md", "a\nb\nc\nd\n")},
			latest: []File{file("notes.md", "a\n\n  b  \nx\n")},
			want:   0.5,
		},
		{
			name:   "repeated lines match once each",
			old:    []File{file("notes.md", "a\na\n")},
			latest: []File{file("notes.md", "a\nb\n")},
			want:   0.5,
		},
		{
			name:   "different paths",
			old:    []File{file("a.md", "same\n")},
			latest: []File{file("b.md", "same\n")},
			want:   0,
		},
		{
			name:   "divided by the larger file count",
			old:    []File{skill("---\nname: pdf\n---\nbody\n")},
			latest: []File{skill("---\nname: pdf\n---\nbody\n"), file("notes.md", "a\n"), file("more.md", "b\n"), file("extra.md", "c\n")},
			want:   0.25,
		},
		{
			name:   "both files empty",
			old:    []File{skill("")},
			latest: []File{skill("")},
			want:   1,
		},
		{
			name:   "one file empty",
			old:    []File{file("notes.md", "")},
			latest: []File{file("notes.md", "a\n")},
			want:   0,
		},
		{
			name:   "blank lines only",
			old:    []File{file("notes.md", "\n\n")},
			latest: []File{file("notes.md", " \n")},
			want:   0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Similarity(test.old, test.latest); got != test.want {
				t.Fatalf("Similarity() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWithModesOf(t *testing.T) {
	files := []File{
		{Path: "SKILL.md", Content: []byte("doc"), Mode: ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: ModeRegular},
		{Path: "new.sh", Content: []byte("#!/bin/sh\n"), Mode: ModeRegular},
	}
	reference := Digests([]File{
		{Path: "SKILL.md", Mode: ModeRegular},
		{Path: "scripts/run.sh", Mode: ModeExecutable},
	})
	adopted := WithModesOf(files, reference)
	want := []Mode{ModeRegular, ModeExecutable, ModeRegular}
	for i, file := range adopted {
		if file.Mode != want[i] || string(file.Content) != string(files[i].Content) {
			t.Fatalf("WithModesOf()[%d] = %+v; want mode %s and the same content", i, file, want[i])
		}
	}
	if files[1].Mode != ModeRegular {
		t.Fatalf("WithModesOf changed its input: %+v", files[1])
	}
}

func TestSameContentIgnoresOnlyModes(t *testing.T) {
	file := func(path, content string, mode Mode) File {
		return File{Path: path, Content: []byte(content), Mode: mode}
	}
	version := []File{file("SKILL.md", "doc", ModeRegular), file("run.sh", "#!/bin/sh\n", ModeExecutable)}
	tests := []struct {
		name string
		disk []File
		want bool
	}{
		{"only modes differ", []File{file("SKILL.md", "doc", ModeRegular), file("run.sh", "#!/bin/sh\n", ModeRegular)}, true},
		{"content differs", []File{file("SKILL.md", "edited", ModeRegular), file("run.sh", "#!/bin/sh\n", ModeRegular)}, false},
		{"a file is added", append(slices.Clone(version), file("notes.md", "notes", ModeRegular)), false},
		{"a file is missing", version[:1], false},
	}
	for _, test := range tests {
		if got := SameContent(Digests(test.disk), Digests(version)); got != test.want {
			t.Errorf("%s: SameContent = %v; want %v", test.name, got, test.want)
		}
		if got := ContentSum(test.disk) == ContentSum(version); got != test.want {
			t.Errorf("%s: same ContentSum = %v; want %v", test.name, got, test.want)
		}
	}
	if Sum(version) == Sum(WithModesOf(version, Digests([]File{file("run.sh", "", ModeRegular)}))) {
		t.Error("Sum ignores modes; want it to tell them apart")
	}
}
