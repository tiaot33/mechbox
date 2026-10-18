package library_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
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
			fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
			if err != nil {
				t.Fatal(err)
			}
			want := []library.File{
				{Path: "SKILL.md", Content: []byte(skillDocument("root", "root body")), Mode: library.ModeRegular},
				{Path: "build/asset.bin", Content: []byte{0, 255, 42}, Mode: library.ModeExecutable},
				{Path: "references/nested/SKILL.md", Content: []byte(skillDocument("nested", "keep")), Mode: library.ModeRegular},
			}
			if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), want) {
				t.Fatalf("root files = %#v", skillFiles(t, lib, fetched[0]))
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
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://source.example/skills.zip"})
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
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://source.example/archive?token=example-secret"})
	if !library.IsCode(err, library.SourceUnreachable) || strings.Contains(err.Error(), "example-secret") {
		t.Fatalf("archive error exposed credentials or wrong code: %v", err)
	}
	// 链接里带用户名密码的,不联网就拒绝,免得凭据写进库里。
	_, err = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://user:example-secret@source.example/archive"})
	if !library.IsCode(err, library.InvalidArgument) || strings.Contains(err.Error(), "example-secret") {
		t.Fatalf("credential link exposed credentials or wrong code: %v", err)
	}
	_, err = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://user:example-secret@source.example/%zz"})
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
	fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"useful"})
	if err != nil || len(fetched) != 1 || string(skillFiles(t, lib, fetched[0])[0].Content) != content {
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
	fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"good"})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("good", "body")), Mode: library.ModeRegular},
		{Path: "build/asset", Content: []byte("keep build asset"), Mode: library.ModeRegular},
		{Path: "references/nested/SKILL.md", Content: []byte(skillDocument("nested", "keep nested")), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{0, 255, 1}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), want) {
		t.Fatalf("files = %#v", skillFiles(t, lib, fetched[0]))
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

// 三条真实来源路径共用同一份内容与技能库断言。
func discoverySources(t *testing.T, files map[string]string, check func(*testing.T, *library.Library, library.PreviewFetchRequest, *gitHubAPIMock, gitHubFixture)) {
	t.Helper()
	for _, source := range []string{"github", "clone", "archive"} {
		t.Run(source, func(t *testing.T) {
			lib := openTestLibrary(t)
			if source == "archive" {
				check(t, lib, serveArchive(t, zipArchive(t, files)), nil, gitHubFixture{})
				return
			}
			entries := map[string]repoFile{}
			for name, content := range files {
				entries[name] = repoFile{content: []byte(content), mode: 0o644}
			}
			repo := writeGitHubRepo(t, entries)
			fx := inspectGitHubRepo(t, repo)
			spec := gitHubRequest()
			var api *gitHubAPIMock
			if source == "github" {
				api = serveGitHubAPI(t, fx)
				blockGitHubClone(t)
			} else {
				serveGitHubCloneOnly(t, repo)
			}
			check(t, lib, spec, api, fx)
		})
	}
}

func TestPreviewDiscoveryPrefersConventionalDirectories(t *testing.T) {
	files := map[string]string{
		"skills/category/useful/SKILL.md": skillDocument("useful", "keep"),
		"skills/a/b/c/d/SKILL.md":         skillDocument("too-deep", "skip"),
		"examples/foo/SKILL.md":           skillDocument("example", "skip"),
		".claude/skills/claude/SKILL.md":  skillDocument("claude", "keep"),
		".codex/skills/codex/SKILL.md":    skillDocument("codex", "keep"),
		".cursor/skills/cursor/SKILL.md":  skillDocument("cursor", "skip"),
	}
	discoverySources(t, files, func(t *testing.T, lib *library.Library, spec library.PreviewFetchRequest, api *gitHubAPIMock, fx gitHubFixture) {
		got, err := lib.PreviewFetch(t.Context(), spec)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, skill := range got.Candidates {
			paths = append(paths, skill.Path)
		}
		want := []string{"skills/category/useful", ".claude/skills/claude", ".codex/skills/codex"}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	})
}

func TestPreviewDiscoveryRootFallbackAndDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name        string
		files       map[string]string
		paths       []string
		diagnostics []string
	}{
		{"overlapping priority directories", map[string]string{"skills/SKILL.md": skillDocument("container", "body"), "skills/.curated/nested/SKILL.md": skillDocument("nested", "body")}, []string{"skills"}, nil},
		{"fallback after diagnostics", map[string]string{"a/SKILL.md": "---\nname: missing-description\n---\n", "a/inner/SKILL.md": skillDocument("inner", "body")}, []string{"a/inner"}, []string{"a"}},
		{"hidden containers extend depth", map[string]string{"skills/.curated/a/b/c/SKILL.md": skillDocument("curated", "body"), "skills/.experimental/x/SKILL.md": skillDocument("experimental", "body"), "skills/.system/x/SKILL.md": skillDocument("system", "body")}, []string{"skills/.experimental/x", "skills/.system/x", "skills/.curated/a/b/c"}, nil},
		{"root short circuit", map[string]string{"SKILL.md": skillDocument("root", "body"), "skills/child/SKILL.md": skillDocument("child", "nested")}, []string{"."}, nil},
		{"invalid root continues", map[string]string{"SKILL.md": "---\nname: root\n---\n", "skills/child/SKILL.md": skillDocument("child", "body")}, []string{"skills/child"}, []string{"."}},
		{"fallback bounded", map[string]string{"examples/foo/SKILL.md": skillDocument("example", "body"), "a/b/c/d/e/SKILL.md": skillDocument("five", "body"), "a/b/c/d/e/f/SKILL.md": skillDocument("six", "body"), "dist/inner/SKILL.md": skillDocument("excluded", "body")}, []string{"a/b/c/d/e", "examples/foo"}, nil},
		{"first duplicate wins", map[string]string{"z/SKILL.md": skillDocument("same", "first"), "skills/a/SKILL.md": skillDocument("same", "second")}, []string{"z"}, []string{"skills/a"}},
		{"excluded directory is skill", map[string]string{"dist/SKILL.md": skillDocument("dist", "body"), "build/inner/SKILL.md": skillDocument("hidden", "body")}, []string{"dist"}, nil},
		{"lowercase filename", map[string]string{"skills/lower/skill.md": skillDocument("lower", "body")}, []string{"skills/lower"}, nil},
		{"required metadata", map[string]string{"a/SKILL.md": "---\nname: a\n---\n", "b/SKILL.md": "---\nname: b\ndescription: 123\n---\n", "c/SKILL.md": skillDocument("valid", "body")}, []string{"c"}, []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discoverySources(t, tc.files, func(t *testing.T, lib *library.Library, spec library.PreviewFetchRequest, api *gitHubAPIMock, fx gitHubFixture) {
				got, err := lib.PreviewFetch(t.Context(), spec)
				if err != nil {
					t.Fatal(err)
				}
				var paths, diagnostics []string
				for _, candidate := range got.Candidates {
					paths = append(paths, candidate.Path)
				}
				for _, d := range got.Diagnostics {
					diagnostics = append(diagnostics, d.Path)
				}
				if !reflect.DeepEqual(paths, tc.paths) || !reflect.DeepEqual(diagnostics, tc.diagnostics) {
					t.Fatalf("candidates = %#v", got.Candidates)
				}
				fetched, err := lib.Fetch(t.Context(), got.ID, candidateNames(got))
				if err != nil || len(fetched) != len(tc.paths) {
					t.Fatalf("fetch = %#v, %v", fetched, err)
				}
				for i, skill := range fetched {
					prefix := tc.paths[i] + "/"
					if tc.paths[i] == "." {
						prefix = ""
					}
					for _, file := range skillFiles(t, lib, skill) {
						if string(file.Content) != tc.files[prefix+file.Path] {
							t.Fatalf("unexpected content: %s/%s", prefix, file.Path)
						}
					}
				}
			})
		})
	}
}

func TestPreviewDiscoveryPluginManifests(t *testing.T) {
	files := map[string]string{
		".claude-plugin/marketplace.json": `{"metadata":{"pluginRoot":"./plugins"},"plugins":[{"name":"alpha","source":"./a","skills":["./skills/x","./special/q","../outside","./../../escape"]},{"name":"remote","source":{"source":"github","repo":"owner/remote"}},{"name":"unsafe","source":"../outside"}]}`,
		".claude-plugin/plugin.json":      `{"name":"single","skills":["./extras/solo"]}`,
		"plugins/a/skills/x/skill.md":     skillDocument("x", "body"),
		"plugins/a/skills/y/SKILL.md":     skillDocument("y", "body"),
		"plugins/a/special/q/SKILL.md":    skillDocument("q", "body"),
		"extras/solo/SKILL.md":            skillDocument("solo", "body"),
		"examples/ignored/SKILL.md":       skillDocument("ignored", "body"),
	}
	discoverySources(t, files, func(t *testing.T, lib *library.Library, spec library.PreviewFetchRequest, api *gitHubAPIMock, fx gitHubFixture) {
		got, err := lib.PreviewFetch(t.Context(), spec)
		if err != nil {
			t.Fatal(err)
		}
		want := []library.Candidate{
			{Name: "x", Path: "plugins/a/skills/x", PluginName: "alpha"},
			{Name: "y", Path: "plugins/a/skills/y"},
			{Name: "q", Path: "plugins/a/special/q", PluginName: "alpha"},
			{Name: "solo", Path: "extras/solo", PluginName: "single"},
		}
		if !reflect.DeepEqual(got.Candidates, want) {
			t.Fatalf("candidates = %#v", got.Candidates)
		}
	})
}

func TestPreviewDiscoverySubpathUsesRelativeConventions(t *testing.T) {
	files := map[string]string{
		".claude-plugin/plugin.json":      `{"name":"wrong-root","skills":["./area/extras/solo"]}`,
		"area/.claude-plugin/plugin.json": `{"name":"subpath","skills":["./extras/solo"]}`,
		"area/skills/demo/SKILL.md":       skillDocument("demo", "body"),
		"area/extras/solo/SKILL.md":       skillDocument("solo", "body"),
		"area/examples/hidden/SKILL.md":   skillDocument("hidden", "body"),
	}
	discoverySources(t, files, func(t *testing.T, lib *library.Library, spec library.PreviewFetchRequest, api *gitHubAPIMock, fx gitHubFixture) {
		if strings.HasPrefix(spec.Input, "https://source.example/") {
			return
		}
		spec.Subpath = "area"
		got, err := lib.PreviewFetch(t.Context(), spec)
		if err != nil || len(got.Candidates) != 2 || got.Candidates[0].Path != "area/skills/demo" || got.Candidates[1].Path != "area/extras/solo" || got.Candidates[1].PluginName != "subpath" {
			t.Fatalf("subpath = %#v, %v", got, err)
		}
		spec.Subpath = "area/skills/demo"
		got, err = lib.PreviewFetch(t.Context(), spec)
		if err != nil || len(got.Candidates) != 1 || got.Candidates[0].Path != "area/skills/demo" {
			t.Fatalf("subpath root = %#v, %v", got, err)
		}
	})
}

func TestPreviewDiscoveryIgnoresInvalidManifestEntries(t *testing.T) {
	for _, manifest := range []string{
		`{`,
		`{"metadata":{"pluginRoot":"plugins"},"plugins":[{"name":"bad","source":"./hidden","skills":["./skills/x"]}]}`,
		`{"metadata":{"pluginRoot":"./plugins"},"plugins":[{"name":"bad","source":"hidden","skills":["./skills/x"]}]}`,
		`{"metadata":{"pluginRoot":"./plugins"},"plugins":[{"name":"bad","source":{"source":"github"},"skills":["./skills/x"]}]}`,
		`{"plugins":[{"name":"bad","source":"./plugins/../plugins/hidden","skills":["./skills/x"]}]}`,
	} {
		t.Run(manifest, func(t *testing.T) {
			files := map[string]string{
				".claude-plugin/marketplace.json":  manifest,
				"skills/good/SKILL.md":             skillDocument("good", "body"),
				"plugins/hidden/skills/x/SKILL.md": skillDocument("hidden", "body"),
			}
			discoverySources(t, files, func(t *testing.T, lib *library.Library, spec library.PreviewFetchRequest, api *gitHubAPIMock, fx gitHubFixture) {
				got, err := lib.PreviewFetch(t.Context(), spec)
				if err != nil || len(got.Candidates) != 1 || got.Candidates[0].Name != "good" || got.Candidates[0].PluginName != "" {
					t.Fatalf("invalid manifest = %#v, %v", got, err)
				}
			})
		})
	}
}

func TestPreviewDiscoveryChoosesSkillFilenameDeterministically(t *testing.T) {
	for _, exact := range []bool{false, true} {
		files := map[string]string{
			"demo/Skill.md": skillDocument("sorted-first", "mixed"),
			"demo/skill.md": skillDocument("lower", "lower"),
		}
		want := "sorted-first"
		if exact {
			files["demo/SKILL.md"] = skillDocument("exact", "upper")
			want = "exact"
		}
		for _, source := range []string{"github", "archive"} {
			t.Run(source+"/"+want, func(t *testing.T) {
				lib := openTestLibrary(t)
				var spec library.PreviewFetchRequest
				if source == "archive" {
					spec = serveArchive(t, zipArchive(t, files))
				} else {
					// 直接构造真实 git tree,不依赖宿主文件系统是否区分大小写。
					repo := writeGitHubRepo(t, map[string]repoFile{"seed": {content: []byte("seed"), mode: 0o644}})
					for name, content := range files {
						temp := filepath.Join(t.TempDir(), "content")
						writeFile(t, temp, []byte(content), 0o644)
						command := exec.Command("git", "-C", repo, "hash-object", "-w", temp)
						output, err := command.Output()
						if err != nil {
							t.Fatal(err)
						}
						runGit(t, repo, "update-index", "--add", "--cacheinfo", "100644", strings.TrimSpace(string(output)), name)
					}
					runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "case tree")
					serveGitHubAPI(t, inspectGitHubRepo(t, repo))
					blockGitHubClone(t)
					spec = gitHubRequest()
				}
				got, err := lib.PreviewFetch(t.Context(), spec)
				if err != nil || len(got.Candidates) != 1 || got.Candidates[0].Name != want {
					t.Fatalf("case selection = %#v, %v", got, err)
				}
				fetched, err := lib.Fetch(t.Context(), got.ID, candidateNames(got))
				if err != nil || len(skillFiles(t, lib, fetched[0])) != len(files) {
					t.Fatalf("case files = %#v, %v", fetched, err)
				}
				for _, file := range skillFiles(t, lib, fetched[0]) {
					if string(file.Content) != files["demo/"+file.Path] {
						t.Fatalf("changed filename/content: %#v", file)
					}
				}
			})
		}
	}
}

func TestPreviewTarArchiveMadeByGitArchive(t *testing.T) {
	isolateGit(t)
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "skills", "pdf", "SKILL.md"), []byte(skillDocument("pdf", "body")), 0o644)
	writeFile(t, filepath.Join(repository, "skills", "pdf", "scripts", "run.sh"), []byte("echo"), 0o755)
	commitGit(t, repository, "initial")
	// git archive 打出的包带 pax 全局头(记录提交)和目录条目,托管平台的源码包也是这样。
	archive := filepath.Join(t.TempDir(), "skills.tar.gz")
	runGit(t, repository, "archive", "--format=tar.gz", "--prefix=repo-main/", "-o", archive, "HEAD")
	content, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	s := serveSite(t)
	s.put("https://downloads.example/skills.tar.gz", content)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://downloads.example/skills.tar.gz"})
	if err != nil || !reflect.DeepEqual(candidateNames(preview), []string{"pdf"}) {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"pdf"})
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("pdf", "body")), Mode: library.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("echo"), Mode: library.ModeExecutable},
	}
	if got := skillFiles(t, lib, fetched[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v", got)
	}
}

func TestPreviewRejectsUnsafeOrBrokenTarArchives(t *testing.T) {
	skill := skillDocument("pdf", "body")
	// entries 打一个含 pdf/SKILL.md 的 tar,再追加 extra 里的条目;普通文件按 Size 填零。
	entries := func(compress bool, extra ...*tar.Header) []byte {
		var buffer bytes.Buffer
		var out io.Writer = &buffer
		zipped := gzip.NewWriter(&buffer)
		if compress {
			out = zipped
		}
		writer := tar.NewWriter(out)
		if err := writer.WriteHeader(&tar.Header{Name: "pdf/SKILL.md", Mode: 0o644, Size: int64(len(skill)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, skill); err != nil {
			t.Fatal(err)
		}
		for _, header := range extra {
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if header.Typeflag == tar.TypeReg {
				if _, err := io.CopyN(writer, infiniteZeros{}, header.Size); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if compress {
			if err := zipped.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return buffer.Bytes()
	}
	valid := entries(true)
	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"symlink entry", entries(false, &tar.Header{Name: "pdf/link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})},
		{"hard link entry", entries(true, &tar.Header{Name: "pdf/link", Linkname: "pdf/SKILL.md", Typeflag: tar.TypeLink})},
		{"device entry", entries(false, &tar.Header{Name: "pdf/null", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3})},
		{"unsafe path", entries(true, &tar.Header{Name: "../escape", Mode: 0o644, Typeflag: tar.TypeReg})},
		{"expands beyond size limit", entries(true, &tar.Header{Name: "pdf/zeros", Mode: 0o644, Size: 64 << 20, Typeflag: tar.TypeReg})},
		{"truncated in file content", entries(false)[:530]},
		{"corrupt entry header", append(entries(false)[:1024], bytes.Repeat([]byte("x"), 512)...)},
		{"truncated gzip", valid[:len(valid)/2]},
		{"corrupt gzip header", append([]byte{0x1f, 0x8b, 0xff}, valid[3:]...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateGit(t)
			s := serveSite(t)
			s.put("https://downloads.example/skills.tar.gz", tc.content)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://downloads.example/skills.tar.gz"})
			if !library.IsCode(err, library.SourceInvalid) || preview.ID != "" {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
		})
	}
}

func TestPreviewGzipThatIsNotTarIsNotTreatedAsArchive(t *testing.T) {
	isolateGit(t)
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	if _, err := io.WriteString(zipped, skillDocument("pdf", "body")); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	s := serveSite(t)
	s.put("https://downloads.example/SKILL.md.gz", buffer.Bytes())
	lib := openTestLibrary(t)
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://downloads.example/SKILL.md.gz"})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "input"})
}
