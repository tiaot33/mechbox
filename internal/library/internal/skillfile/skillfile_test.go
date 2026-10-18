package skillfile

import "testing"

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
