package library_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func fetchArchive(t *testing.T, lib *library.Library, spec library.PreviewFetchRequest) []library.Skill {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return fetchSkills(t, lib, preview, candidateNames(preview))
}

// 服务端给出各种写法的 validator 并回 304 时,检查与更新都判定无更新,只刷新读取时间。
func TestURLNotModifiedMeansNoUpdate(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	for _, tc := range []struct{ name, etag, lastModified string }{
		{"strong etag", `"v1"`, ""},
		{"weak etag", `W/"weak-v1"`, ""},
		{"unquoted etag", `unquoted-v1`, ""},
		{"last modified", "", "Tue, 15 Sep 2026 10:00:00 GMT"},
		{"malformed last modified", "", "yesterday-ish"},
		{"no validator", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newArchiveServer(t, archive, tc.etag)
			if tc.lastModified != "" {
				server.serveLastModified(archive, tc.lastModified)
			}
			lib := openTestLibrary(t)
			id := fetchArchive(t, lib, server.request())[0].ID
			before, err := lib.GetSkill(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateCurrent {
				t.Fatalf("CheckUpdates = %v, %v", status, err)
			}
			result, err := updateSkill(t, lib, t.Context(), id)
			if err != nil || result.Changed {
				t.Fatalf("Update = %#v, %v", result, err)
			}
			if result.Skill.Revision == "" || result.Skill.Revision != before.Revision || !result.Skill.UpdatedAt.IsZero() {
				t.Fatalf("skill after no-op update = %#v, before %#v", result.Skill, before)
			}
			if versions, err := lib.ListVersions(t.Context(), id); err != nil || len(versions) != 1 {
				t.Fatalf("versions = %#v, %v", versions, err)
			}
		})
	}
}

func TestURLUpdateWritesChangedArchive(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")}), `"v1"`)
	lib := openTestLibrary(t)
	id := fetchArchive(t, lib, server.request())[0].ID

	server.serve(zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")}), `"v2"`)
	if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateAvailable {
		t.Fatalf("CheckUpdates after remote change = %v, %v", status, err)
	}
	result, err := updateSkill(t, lib, t.Context(), id)
	if err != nil || !result.Changed || string(skillFiles(t, lib, result.Skill)[0].Content) != skillDocument("demo", "new") {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	if versions, err := lib.ListVersions(t.Context(), id); err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateCurrent {
		t.Fatalf("CheckUpdates after Update = %v, %v", status, err)
	}
}

// 同一压缩包里的技能各自判断:只更新了其中一个后,另一个仍报告有更新。
func TestURLSkillsFromOneArchiveAreCheckedIndependently(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "old"),
	}), `"v1"`)
	lib := openTestLibrary(t)
	ids := skillIDs(fetchArchive(t, lib, server.request()))
	requireAvailable := func(want ...bool) {
		t.Helper()
		for i, id := range ids {
			status, err := checkSkillUpdate(t, lib, t.Context(), id)
			if err != nil || (status == library.UpdateAvailable) != want[i] || (status != library.UpdateAvailable && status != library.UpdateCurrent) {
				t.Fatalf("CheckUpdates(%d) = %v, %v; want available %v", id, status, err, want[i])
			}
		}
	}
	requireAvailable(false, false)

	server.serve(zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "new"),
	}), `"v2"`)
	requireAvailable(false, true)

	server.serve(zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "new"),
		"second/SKILL.md": skillDocument("second", "new"),
	}), `"v3"`)
	if result, err := updateSkill(t, lib, t.Context(), ids[0]); err != nil || !result.Changed {
		t.Fatalf("Update first = %#v, %v", result, err)
	}
	requireAvailable(false, true)
}

// 没带条件头却收到 304 是远端异常,不能当成"无更新"。
func TestURLUnexpectedNotModifiedIsAnError(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := newArchiveServer(t, archive, "")
	lib := openTestLibrary(t)
	id := fetchArchive(t, lib, server.request())[0].ID
	server.serveNotModified()
	if preview, err := lib.PreviewFetch(t.Context(), server.request()); !library.IsCode(err, library.SourceUnreachable) || preview.ID != "" {
		t.Fatalf("PreviewFetch = %#v, %v, want SourceUnreachable", preview, err)
	}
	if status, err := checkSkillUpdate(t, lib, t.Context(), id); !library.IsCode(err, library.SourceUnreachable) || status != library.UpdateFailed {
		t.Fatalf("CheckUpdates = %v, %v, want SourceUnreachable", status, err)
	}
}

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
