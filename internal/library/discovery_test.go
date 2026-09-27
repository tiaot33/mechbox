package library_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestPreviewRootSkillPreservesAllAssetsAndRejectsUnsafeArchives(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra string
		mode  os.FileMode
		valid bool
	}{
		{"root content", "build/asset.bin", 0o755, true},
		{"unsafe path", "../escape", 0o644, false},
		{"duplicate", "SKILL.md", 0o644, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for _, file := range []struct {
				name, content string
				mode          os.FileMode
			}{
				{"SKILL.md", skillDocument("root", "root body"), 0o644},
				{"references/nested/SKILL.md", skillDocument("nested", "keep"), 0o644},
				{".git/config", "excluded", 0o644},
				{tc.extra, string([]byte{0, 255, 42}), tc.mode},
			} {
				header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
				header.SetMode(file.mode)
				entry, err := writer.CreateHeader(header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(entry, file.content); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), serveArchive(t, buffer.Bytes()))
			if !tc.valid {
				if !library.IsCode(err, library.SourceInvalid) || preview.ID != "" {
					t.Fatalf("unsafe archive = %#v, %v", preview, err)
				}
				return
			}
			if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Path != "." {
				t.Fatalf("root = %#v, %v", preview, err)
			}
			fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
			if err != nil {
				t.Fatal(err)
			}
			want := []library.File{
				{Path: "SKILL.md", Content: []byte(skillDocument("root", "root body")), Mode: library.ModeRegular},
				{Path: "build/asset.bin", Content: []byte{0, 255, 42}, Mode: library.ModeExecutable},
				{Path: "references/nested/SKILL.md", Content: []byte(skillDocument("nested", "keep")), Mode: library.ModeRegular},
			}
			if !reflect.DeepEqual(fetched[0].Files, want) {
				t.Fatalf("root files = %#v", fetched[0].Files)
			}
		})
	}
}

func TestPreviewArchiveExcludesBrokenCandidateAndEnforcesExpandedLimit(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range []struct {
		name, content string
		mode          os.FileMode
	}{
		{"good/SKILL.md", skillDocument("good", "body"), 0o644},
		{"bad/SKILL.md", skillDocument("bad", "body"), 0o644},
		{"bad/link", "/outside", os.ModeSymlink | 0o777},
		{"unrelated-link", "/outside", os.ModeSymlink | 0o777},
	} {
		header := &zip.FileHeader{Name: file.name}
		header.SetMode(file.mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, file.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), serveArchive(t, buffer.Bytes()))
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "good" || len(preview.Diagnostics) != 1 || preview.Diagnostics[0].Path != "bad" {
		t.Fatalf("archive candidates = %#v, %v", preview, err)
	}
	buffer.Reset()
	writer = zip.NewWriter(&buffer)
	if _, err := writer.CreateRaw(&zip.FileHeader{Name: "ignored/huge", UncompressedSize64: 65 << 20}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.PreviewFetch(t.Context(), serveArchive(t, buffer.Bytes())); !library.IsCode(err, library.SourceInvalid) {
		t.Fatalf("expanded limit = %v", err)
	}
}

func TestPreviewArchiveEnforcesExpandedFileCountLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files int
		ok    bool
	}{
		{"at limit", 1000, true},
		{"over limit", 1001, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for i := range tc.files {
				name := "good/SKILL.md"
				content := skillDocument("good", "body")
				if i > 0 {
					name = fmt.Sprintf("extra/%d.txt", i)
					content = "x"
				}
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(entry, content); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), serveArchive(t, buffer.Bytes()))
			if !tc.ok {
				if !library.IsCode(err, library.SourceInvalid) || preview.ID != "" {
					t.Fatalf("file count limit = %#v, %v", preview, err)
				}
				if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
					t.Fatalf("Library = %v, %v", skills, err)
				}
				return
			}
			if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "good" {
				t.Fatalf("at limit = %#v, %v", preview, err)
			}
		})
	}
}

type infiniteZeros struct{}

func (infiniteZeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestPreviewArchiveRejectsOversizedDownloadWithoutCreatingPreview(t *testing.T) {
	lib := openTestLibrary(t)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(io.LimitReader(infiniteZeros{}, 64<<20+1)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: "https://source.example/skills.zip"})
	if !library.IsCode(err, library.SourceInvalid) || preview.ID != "" {
		t.Fatalf("download size = %#v, %v", preview, err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("Library = %v, %v", skills, err)
	}
}

func TestPreviewArchiveErrorsDoNotExposeSourceCredentials(t *testing.T) {
	lib := openTestLibrary(t)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	t.Cleanup(func() { http.DefaultClient = client })
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: "https://user:example-secret@source.example/archive?token=example-secret"})
	if !library.IsCode(err, library.SourceUnreachable) || strings.Contains(err.Error(), "example-secret") {
		t.Fatalf("archive error exposed credentials or wrong code: %v", err)
	}
	_, err = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: "https://user:example-secret@source.example/%zz"})
	if !library.IsCode(err, library.InvalidArgument) || strings.Contains(err.Error(), "example-secret") {
		t.Fatalf("invalid URL exposed credentials or wrong code: %v", err)
	}
}

func TestPreviewDescribesValidCandidatesAndDiagnosesInvalidFrontmatter(t *testing.T) {
	content := "---\nname: useful\ndescription: >-\n  Helps with\n  useful work.\n---\n# Original\n"
	spec := serveArchive(t, zipArchive(t, map[string]string{
		"one/SKILL.md": content,
		"two/SKILL.md": "---\nname: minimal\n---\n",
		"bad/SKILL.md": "---\nname: broken\ndescription: [invalid]\n---\n",
	}))
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Candidate{{Name: "useful", Description: "Helps with useful work.", Path: "one"}}
	if !reflect.DeepEqual(preview.Candidates, want) || len(preview.Diagnostics) != 2 || preview.Diagnostics[0].Path != "bad" {
		t.Fatalf("PreviewFetch = %#v", preview)
	}
	preview.Candidates[0].Name = "mutated"
	preview.Diagnostics[0].Reason = "mutated"
	fetched, err := getFetchedSkills(t.Context(), lib, preview, []string{"useful"})
	if err != nil || len(fetched) != 1 || string(fetched[0].Files[0].Content) != content {
		t.Fatalf("fetch = %#v, %v", fetched, err)
	}
}

func skillDocument(name, body string) string {
	return "---\nname: " + name + "\ndescription: \"\"\n---\n" + body
}

func TestPreviewDiscoversCandidatesBeforeReadingUnrelatedFiles(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	for _, root := range []string{"skills/good", ".claude/skills/other", "bad", "node_modules/ignored", "dist/ignored"} {
		writeFile(t, filepath.Join(repository, root, "SKILL.md"), []byte(skillDocument(filepath.Base(root), "body")), 0o644)
	}
	writeFile(t, filepath.Join(repository, "skills/good/scripts/run"), []byte{0, 255, 1}, 0o755)
	writeFile(t, filepath.Join(repository, "skills/good/build/asset"), []byte("keep build asset"), 0o644)
	writeFile(t, filepath.Join(repository, "skills/good/references/nested/SKILL.md"), []byte(skillDocument("nested", "keep nested")), 0o644)
	for _, link := range []string{"unrelated-link", "bad/link"} {
		if err := os.Symlink("/does-not-exist", filepath.Join(repository, link)); err != nil {
			t.Fatal(err)
		}
	}
	commitGit(t, repository, "candidates")
	serveGitHubCloneOnly(t, repository)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 2 || preview.Candidates[0].Name != "good" || preview.Candidates[1].Name != "other" || len(preview.Diagnostics) != 1 || preview.Diagnostics[0].Path != "bad" {
		t.Fatalf("PreviewFetch = %#v", preview)
	}
	fetched, err := getFetchedSkills(t.Context(), lib, preview, []string{"good"})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("good", "body")), Mode: library.ModeRegular},
		{Path: "build/asset", Content: []byte("keep build asset"), Mode: library.ModeRegular},
		{Path: "references/nested/SKILL.md", Content: []byte(skillDocument("nested", "keep nested")), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{0, 255, 1}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(fetched[0].Files, want) {
		t.Fatalf("files = %#v", fetched[0].Files)
	}
}

func TestPreviewRejectsInvalidCandidateSetsWithoutCreatingPreview(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  map[string]string
		detail string
	}{
		{"invalid YAML", map[string]string{"bad/SKILL.md": "---\nname: [broken\n---\n"}, "bad"},
		{"missing name", map[string]string{"bad/SKILL.md": "---\ndescription: okay\n---\n"}, "name"},
		{"numeric name", map[string]string{"bad/SKILL.md": "---\nname: 123\n---\n"}, "name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), serveArchive(t, zipArchive(t, tc.files)))
			if !library.IsCode(err, library.SourceInvalid) || !strings.Contains(err.Error(), tc.detail) || preview.ID != "" {
				t.Fatalf("PreviewFetch = %#v, %v", preview, err)
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("Library = %v, %v", skills, err)
			}
		})
	}
}
