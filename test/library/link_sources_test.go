package library_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// site 接管 http.DefaultClient,按完整 URL 回答固定内容,其余一律 404,并记下每个 URL 被请求的次数。
type site struct {
	mu       sync.Mutex
	pages    map[string][]byte
	etags    map[string]string
	statuses map[string]int
	hits     map[string]int
}

func serveSite(t *testing.T) *site {
	t.Helper()
	s := &site{pages: map[string][]byte{}, etags: map[string]string{}, statuses: map[string]int{}, hits: map[string]int{}}
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(s.roundTrip)}
	t.Cleanup(func() { http.DefaultClient = client })
	return s
}

func (s *site) put(url string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[url] = body
}

func (s *site) putWithETag(url string, body []byte, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[url], s.etags[url] = body, etag
}

// fail 让 url 一律回 status。
func (s *site) fail(url string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[url] = status
}

func (s *site) remove(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pages, url)
}

func (s *site) count(url string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[url]
}

func (s *site) roundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	url := r.URL.String()
	s.hits[url]++
	if status := s.statuses[url]; status != 0 {
		return textResponse(status, "failure"), nil
	}
	body, ok := s.pages[url]
	if !ok {
		return textResponse(http.StatusNotFound, "<html>not found</html>"), nil
	}
	header := make(http.Header)
	if etag := s.etags[url]; etag != "" {
		header.Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			return &http.Response{StatusCode: http.StatusNotModified, Header: header, Body: http.NoBody}, nil
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

// isolateGit 让测试里的 git 不读本机配置,并把所有 https 远端改写到不存在的本地路径,git ls-remote 因此必然失败且不联网。
func isolateGit(t *testing.T) string {
	t.Helper()
	config := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, t.TempDir(), "config", "--file", config, "url.file://"+filepath.Join(t.TempDir(), "missing")+"/.insteadOf", "https://")
	return config
}

// forbidGit 把 PATH 里的 git 换成一个只记下被调用过、然后失败的脚本。
func forbidGit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	writeFile(t, filepath.Join(dir, "git"), []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func tarGz(t *testing.T, files map[string]string, compress bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var out io.Writer = &buffer
	var zipped *gzip.Writer
	if compress {
		zipped = gzip.NewWriter(&buffer)
		out = zipped
	}
	writer := tar.NewWriter(out)
	for name, content := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if zipped != nil {
		if err := zipped.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buffer.Bytes()
}

func describedSkill(name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestRawSkillFileLinkPreviewsOneSkillWithoutCloning(t *testing.T) {
	marker := forbidGit(t)
	s := serveSite(t)
	link := "https://raw.githubusercontent.com/owner/repo/main/skills/demo/SKILL.md"
	s.put(link, []byte(describedSkill("demo", "Raw skill", "body")))
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: link})
	if err != nil {
		t.Fatal(err)
	}
	if want := (library.Source{Type: library.SourceURL, Label: link}); preview.Source != want {
		t.Fatalf("source = %#v; want %#v", preview.Source, want)
	}
	if want := []library.Candidate{{Name: "demo", Description: "Raw skill", Path: "."}}; !reflect.DeepEqual(preview.Candidates, want) {
		t.Fatalf("candidates = %#v; want %#v", preview.Candidates, want)
	}
	fetched := fetchSkills(t, lib, preview, []string{"demo"})
	if files := skillFiles(t, lib, fetched[0]); len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatalf("files = %#v; want only SKILL.md", files)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("raw link ran git: %v", err)
	}
}

func TestDirectDownloadRecognizesArchivesByContent(t *testing.T) {
	isolateGit(t)
	files := map[string]string{"pack/alpha/SKILL.md": skillDocument("alpha", "a"), "pack/beta/SKILL.md": skillDocument("beta", "b"), "pack/beta/notes.txt": "notes"}
	for _, tc := range []struct {
		name    string
		link    string
		content []byte
	}{
		{"zip without extension", "https://downloads.example/get?id=1", zipArchive(t, files)},
		{"tar.gz", "https://downloads.example/skills.tar.gz", tarGz(t, files, true)},
		{"tar", "https://downloads.example/latest", tarGz(t, files, false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serveSite(t)
			s.putWithETag(tc.link, tc.content, `"v1"`)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: tc.link})
			if err != nil {
				t.Fatal(err)
			}
			if preview.Source.Type != library.SourceURL || preview.Source.Label != tc.link || preview.Source.Ref != "" {
				t.Fatalf("source = %#v", preview.Source)
			}
			if got := candidateNames(preview); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
				t.Fatalf("candidates = %v", got)
			}
			fetched := fetchSkills(t, lib, preview, []string{"beta"})
			if files := skillFiles(t, lib, fetched[0]); len(files) != 2 || files[0].Path != "SKILL.md" || files[1].Path != "notes.txt" {
				t.Fatalf("files = %#v", files)
			}
			// 检查更新和现有的 zip 地址一样,按 HTTP validator 发条件请求。
			if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateCurrent {
				t.Fatalf("check = %s, %v", status, err)
			}
		})
	}
}

func TestLinkPreviewReportsWhatTheLinkIsNot(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	s.put("https://docs.example/guide", []byte("<html>docs</html>"))
	lib := openTestLibrary(t)
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example/guide"})
	typed := requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "input"})
	for _, want := range []string{"skill file or archive", "well-known", "Git repository"} {
		if !strings.Contains(typed.Message, want) {
			t.Fatalf("message %q lacks %q", typed.Message, want)
		}
	}
	for _, index := range []string{
		"https://docs.example/guide/.well-known/agent-skills/index.json",
		"https://docs.example/guide/.well-known/skills/index.json",
		"https://docs.example/.well-known/agent-skills/index.json",
		"https://docs.example/.well-known/skills/index.json",
	} {
		if s.count(index) != 1 {
			t.Fatalf("index %s requested %d times; want once", index, s.count(index))
		}
	}
}

// legacyIndex 写一个旧格式索引:每个技能列出文件清单,文件放在索引旁的 <name>/<file>。
func legacyIndex(t *testing.T, s *site, base string, skills map[string]map[string]string) {
	t.Helper()
	type entry struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Files       []string `json:"files"`
	}
	var entries []entry
	for name, files := range skills {
		e := entry{Name: name, Description: name + " from index"}
		for file, content := range files {
			e.Files = append(e.Files, file)
			s.put(base+"/.well-known/skills/"+name+"/"+file, []byte(content))
		}
		entries = append(entries, e)
	}
	s.put(base+"/.well-known/skills/index.json", mustJSON(t, map[string]any{"skills": entries}))
}

func TestWellKnownSiteLinkPreviewsIndexedSkills(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	s.put("https://docs.example/", []byte("<html>home</html>"))
	legacyIndex(t, s, "https://docs.example", map[string]map[string]string{
		"alpha": {"SKILL.md": describedSkill("alpha", "Alpha", "a"), "references/guide.md": "guide"},
		"beta":  {"SKILL.md": describedSkill("beta", "Beta", "b")},
	})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example/"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (library.Source{Type: library.SourceWellKnown, Label: "docs.example", ViewURL: "https://docs.example"}); preview.Source != want {
		t.Fatalf("source = %#v; want %#v", preview.Source, want)
	}
	if got := candidateNames(preview); !reflect.DeepEqual(got, []string{"alpha", "beta"}) && !reflect.DeepEqual(got, []string{"beta", "alpha"}) {
		t.Fatalf("candidates = %v", got)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha"})
	if fetched[0].Description != "Alpha" || fetched[0].Source.Type != library.SourceWellKnown {
		t.Fatalf("fetched = %#v", fetched[0])
	}
	files := skillFiles(t, lib, fetched[0])
	if len(files) != 2 || files[0].Path != "SKILL.md" || files[1].Path != "references/guide.md" || string(files[1].Content) != "guide" {
		t.Fatalf("files = %#v", files)
	}
}

func TestWellKnownLinkWithPathOnlyCollectsSkillsUnderThatPath(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	legacyIndex(t, s, "https://docs.example", map[string]map[string]string{"root-skill": {"SKILL.md": describedSkill("root-skill", "Root", "r")}})
	legacyIndex(t, s, "https://docs.example/guides", map[string]map[string]string{"guide-skill": {"SKILL.md": describedSkill("guide-skill", "Guide", "g")}})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example/guides"})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateNames(preview); !reflect.DeepEqual(got, []string{"guide-skill"}) || preview.Source.Label != "docs.example/guides" {
		t.Fatalf("scoped preview = %v, %#v", got, preview.Source)
	}
	_, err = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example/blog"})
	typed := requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Name: "/blog"})
	if !strings.Contains(typed.Message, "/blog") {
		t.Fatalf("message = %q", typed.Message)
	}
}

func TestLegacyWellKnownMissingFileFailsFetchAndCheck(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	legacyIndex(t, s, "https://docs.example", map[string]map[string]string{
		"alpha": {"SKILL.md": describedSkill("alpha", "Alpha", "a"), "scripts/run.sh": "echo run"},
	})
	helper := "https://docs.example/.well-known/skills/alpha/scripts/run.sh"
	s.remove(helper)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example"})
	if err != nil {
		t.Fatal(err)
	}
	// 缺了一个辅助文件:不收残缺的版本,整批失败,写明技能和文件。
	_, err = lib.Fetch(t.Context(), preview.ID, []string{"alpha"})
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Name: "alpha", Path: "scripts/run.sh"})
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("skills after failed fetch = %v, %v", skills, err)
	}
	s.put(helper, []byte("echo run"))
	fetched := fetchSkills(t, lib, preview, []string{"alpha"})
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateCurrent {
		t.Fatalf("check = %s, %v", status, err)
	}
	// 检查更新时同一个文件又取不到:结论是来源暂时读不到,不是有变化,也不是来源里已删除。
	s.remove(helper)
	status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID)
	if status != library.UpdateFailed {
		t.Fatalf("check with missing file = %s; want failed", status)
	}
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Name: "alpha", Path: "scripts/run.sh"})
}

// artifactIndex 写一个新格式索引:每个技能一个 SKILL.md 或压缩包地址,带整个文件的 digest。
type artifact struct {
	name    string
	kind    string
	content []byte
	digest  string
}

func artifactIndex(t *testing.T, s *site, base string, artifacts []artifact) {
	t.Helper()
	var entries []map[string]string
	for _, a := range artifacts {
		location := base + "/.well-known/agent-skills/" + a.name + "/artifact"
		s.put(location, a.content)
		digest := a.digest
		if digest == "" {
			digest = digestOf(a.content)
		}
		entries = append(entries, map[string]string{"name": a.name, "type": a.kind, "description": a.name + " from index", "url": a.name + "/artifact", "digest": digest})
	}
	s.put(base+"/.well-known/agent-skills/index.json", mustJSON(t, map[string]any{"$schema": "https://schemas.agentskills.io/discovery/0.2.0/schema.json", "skills": entries}))
}

func TestWellKnownDigestMismatchFailsFetchNamingTheSkill(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	artifactIndex(t, s, "https://docs.example", []artifact{
		{name: "good", kind: "skill-md", content: []byte(describedSkill("good", "Good", "g"))},
		{name: "tampered", kind: "skill-md", content: []byte(describedSkill("tampered", "T", "t")), digest: digestOf([]byte("something else"))},
	})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.Fetch(t.Context(), preview.ID, []string{"good", "tampered"})
	typed := requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Name: "tampered"})
	if !strings.Contains(typed.Message, "digest") {
		t.Fatalf("message = %q", typed.Message)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("skills after failed fetch = %v, %v", skills, err)
	}
}

func TestWellKnownUnchangedDigestSkipsDownloadOnCheck(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	archive := tarGz(t, map[string]string{"SKILL.md": describedSkill("packed", "Packed", "p"), "assets/data.txt": "data"}, true)
	artifacts := []artifact{
		{name: "single", kind: "skill-md", content: []byte(describedSkill("single", "Single", "s"))},
		{name: "packed", kind: "archive", content: archive},
	}
	artifactIndex(t, s, "https://docs.example", artifacts)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"packed", "single"})
	if files := skillFiles(t, lib, fetched[0]); len(files) != 2 || files[0].Path != "SKILL.md" || files[1].Path != "assets/data.txt" {
		t.Fatalf("archive files = %#v", files)
	}
	packed := "https://docs.example/.well-known/agent-skills/packed/artifact"
	single := "https://docs.example/.well-known/agent-skills/single/artifact"
	before := s.count(packed) + s.count(single)
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateCurrent {
		t.Fatalf("check = %s, %v", status, err)
	}
	if after := s.count(packed) + s.count(single); after != before {
		t.Fatalf("check downloaded %d artifacts; want none", after-before)
	}
	// digest 变了才下载,内容真的变了算有新版本。
	artifacts[1].content = tarGz(t, map[string]string{"SKILL.md": describedSkill("packed", "Packed", "changed"), "assets/data.txt": "data"}, true)
	artifactIndex(t, s, "https://docs.example", artifacts)
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateAvailable {
		t.Fatalf("check after change = %s, %v", status, err)
	}
	if s.count(packed) == 0 || s.count(single) != 1 {
		t.Fatalf("downloads after change: packed %d, single %d", s.count(packed), s.count(single))
	}
}

func TestSearchResultFromWellKnownSiteCanBePreviewed(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	s.put("https://skills.sh/api/search?limit=20&q=docs", []byte(`{"skills":[{"id":"docs.example/alpha","name":"alpha","source":"docs.example"},{"id":"owner/repo/beta","name":"beta","source":"owner/repo"}]}`))
	artifactIndex(t, s, "https://docs.example", []artifact{
		{name: "alpha", kind: "skill-md", content: []byte(describedSkill("alpha", "Alpha", "a"))},
		{name: "other", kind: "skill-md", content: []byte(describedSkill("other", "Other", "o"))},
	})
	lib := openTestLibrary(t)
	result, err := lib.SearchSkills(t.Context(), "docs")
	if err != nil || len(result) != 2 {
		t.Fatalf("search = %#v, %v", result, err)
	}
	if want := (library.PreviewFetchRequest{Input: "https://docs.example/.well-known/agent-skills/alpha"}); result[0].Preview != want {
		t.Fatalf("well-known preview request = %#v; want %#v", result[0].Preview, want)
	}
	preview, err := lib.PreviewFetch(t.Context(), result[0].Preview)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateNames(preview); !reflect.DeepEqual(got, []string{"alpha"}) || preview.Source.Type != library.SourceWellKnown || preview.Source.Label != "docs.example" {
		t.Fatalf("preview = %v, %#v", got, preview.Source)
	}
}

func TestGitLabReleaseDownloadLinkIsDirectDownload(t *testing.T) {
	marker := forbidGit(t)
	s := serveSite(t)
	link := "https://gitlab.com/group/repo/-/releases/v1/downloads/skills.zip"
	s.put(link, zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "body")}))
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: link})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Source.Type != library.SourceURL || !reflect.DeepEqual(candidateNames(preview), []string{"demo"}) {
		t.Fatalf("preview = %#v", preview)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("release link ran git: %v", err)
	}
}

func TestLinkWithCredentialsIsRejectedBeforeContactingIt(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	link := "https://user:secret@corp.example/org/repo"
	lib := openTestLibrary(t)
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: link})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "input"})
	if len(s.hits) != 0 {
		t.Fatalf("requests = %v; want none", s.hits)
	}
}

func TestWellKnownIndexFailureIsNotTreatedAsMissingIndex(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	legacyIndex(t, s, "https://docs.example", map[string]map[string]string{"root-skill": {"SKILL.md": describedSkill("root-skill", "Root", "r")}})
	s.fail("https://docs.example/guides/.well-known/agent-skills/index.json", http.StatusInternalServerError)
	lib := openTestLibrary(t)
	// 路径下的索引读取失败不能当成「这个路径下没有技能」,也不能转而用旧路径或站点根的索引。
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example/guides"})
	requireLibraryError(t, err, library.SourceUnreachable)
}

func TestWellKnownBrokenIndexEntryIsUnreadableNotDeleted(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	legacyIndex(t, s, "https://docs.example", map[string]map[string]string{
		"alpha": {"SKILL.md": describedSkill("alpha", "Alpha", "a")},
		"beta":  {"SKILL.md": describedSkill("beta", "Beta", "b")},
	})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha"})
	// 索引还列着 alpha,但这一条坏了(没列 SKILL.md):这次读不到,不是来源里已删除。
	s.put("https://docs.example/.well-known/skills/index.json", []byte(`{"skills":[{"name":"alpha","description":"Alpha","files":["notes.md"]},{"name":"beta","description":"Beta","files":["SKILL.md"]}]}`))
	status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID)
	if status != library.UpdateFailed {
		t.Fatalf("check with broken entry = %s; want failed", status)
	}
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Name: "alpha"})
	again, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example"})
	if err != nil || !reflect.DeepEqual(candidateNames(again), []string{"beta"}) || len(again.Diagnostics) != 1 || again.Diagnostics[0].Path != "alpha" {
		t.Fatalf("preview with broken entry = %#v, %v", again, err)
	}
}

func TestWellKnownArchivesShareOneExtractionBudget(t *testing.T) {
	isolateGit(t)
	s := serveSite(t)
	many := func(name string) []byte {
		files := map[string]string{"SKILL.md": describedSkill(name, name, "body")}
		for i := range 600 {
			files["assets/"+strings.Repeat("x", 3)+string(rune('a'+i%26))+"/"+itoa(i)] = "x"
		}
		return tarGz(t, files, true)
	}
	artifactIndex(t, s, "https://docs.example", []artifact{{name: "first", kind: "archive", content: many("first")}, {name: "second", kind: "archive", content: many("second")}})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://docs.example"})
	if err != nil {
		t.Fatal(err)
	}
	// 两个包各 601 个文件,合起来超过一次入库 1000 个文件的上限;重试时已读过的包同样计入。
	for range 2 {
		_, err = lib.Fetch(t.Context(), preview.ID, []string{"first", "second"})
		requireLibraryError(t, err, library.SourceInvalid)
	}
	if fetched := fetchSkills(t, lib, preview, []string{"first"}); len(skillFiles(t, lib, fetched[0])) != 601 {
		t.Fatalf("single archive was not fetched completely")
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
