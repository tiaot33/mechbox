package library_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// 按名字去 skills.sh 搜,认出没有来源记录的技能的来源

// searchedSite 在 site 之上记下请求:GitHub API 交给 api,其余按完整 URL 由 site 回答;每个请求停一会儿,
// 记下同时在进行的请求最多有几个。
type searchedSite struct {
	*site
	api      *gitHubAPIMock
	delay    time.Duration
	mu       sync.Mutex
	paths    []string
	inFlight atomic.Int32
	peak     atomic.Int32
}

func serveSearchedSite(t *testing.T, api *gitHubAPIMock) *searchedSite {
	t.Helper()
	s := &searchedSite{site: &site{pages: map[string][]byte{}, etags: map[string]string{}, statuses: map[string]int{}, hits: map[string]int{}}, api: api}
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(s.roundTrip)}
	t.Cleanup(func() { http.DefaultClient = client })
	return s
}

func (s *searchedSite) roundTrip(r *http.Request) (*http.Response, error) {
	now := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for peak := s.peak.Load(); now > peak && !s.peak.CompareAndSwap(peak, now); peak = s.peak.Load() {
	}
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Host+r.URL.Path)
	s.mu.Unlock()
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if r.URL.Host == "api.github.com" && s.api != nil {
		return s.api.roundTrip(r)
	}
	return s.site.roundTrip(r)
}

func (s *searchedSite) requested(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, each := range s.paths {
		if each == path {
			count++
		}
	}
	return count
}

// searchResults 让 skills.sh 对 query 的搜索返回 results,每项是 [名字, 来源]。
func (s *searchedSite) searchResults(t *testing.T, query string, results ...[2]string) {
	t.Helper()
	var skills []map[string]string
	for _, result := range results {
		skills = append(skills, map[string]string{"id": result[1] + "/" + result[0], "name": result[0], "source": result[1]})
	}
	s.put("https://skills.sh/api/search?"+url.Values{"q": {query}, "limit": {"20"}}.Encode(), mustJSON(t, map[string]any{"skills": skills}))
}

func findUnmanagedSources(t *testing.T, lib *library.Library, folders ...library.FolderInScope) []library.FoundUnmanagedSource {
	t.Helper()
	found, err := lib.FindUnmanagedSources(t.Context(), library.FindUnmanagedSourcesRequest{Folders: folders})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestFindUnmanagedSourcesRecognizesSkillsWithoutSourceRecords(t *testing.T) {
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	redirectGitHubClone(t, f.repo)
	s := serveSearchedSite(t, f.api)
	directory := filepath.Dir(f.folder)
	// pdf 和来源里的一样;docx 在来源那一版上加了一行;notes 搜到的同名技能内容差得远;mine 搜不到。
	f.writeDisk(t, "v1")
	writeFile(t, filepath.Join(directory, "docx", "SKILL.md"), []byte(skillDocument("docx", "v1\nmy own note")), 0o644)
	writeFile(t, filepath.Join(directory, "notes", "SKILL.md"), []byte(skillDocument("notes", "one\ntwo\nthree\nfour\nfive")), 0o644)
	writeUnmanagedSkill(t, directory, "mine", "hand written")
	// site 有 npx skills 的来源记录,认得出来源,不再去搜。
	writeUnmanagedSkill(t, directory, "site", "from a site")
	writeSourceRecords(t, filepath.Join(f.home, ".agents", ".skill-lock.json"), 3, map[string]map[string]string{
		"site": {"source": "docs.example", "sourceType": "well-known", "sourceUrl": "https://docs.example"},
	})
	s.searchResults(t, "pdf", [2]string{"pdf-tools", "owner/repo"}, [2]string{"pdf", "owner/repo"})
	s.searchResults(t, "docx", [2]string{"docx", "owner/repo"}, [2]string{"docx", "https://gitlab.com/owner/repo"})
	s.searchResults(t, "notes", [2]string{"notes", "docs.example"})
	s.searchResults(t, "mine")
	artifactIndex(t, s.site, "https://docs.example", []artifact{{name: "notes", kind: "skill-md", content: []byte(skillDocument("notes", "something\nelse\nentirely"))}})

	// 列出未纳管技能与整批计划都不联网。
	unmanaged := listUnmanagedSkills(t, f.lib)
	planFetchUnmanaged(t, f.lib, f.claude)
	if len(s.paths) != 0 {
		t.Fatalf("listing requested %v", s.paths)
	}
	var folders []library.FolderInScope
	for _, skill := range unmanaged {
		folders = append(folders, library.FolderInScope{Scope: skill.Scope, Name: skill.Name})
	}
	found := findUnmanagedSources(t, f.lib, folders...)
	want := []library.FoundUnmanagedSource{
		{Scope: f.claude, Name: "docx", SourceInput: "owner/repo"},
		{Scope: f.claude, Name: "pdf", SourceInput: "owner/repo"},
	}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("found = %#v, want %#v", found, want)
	}
	if got := s.requested("skills.sh/api/search"); got != 4 {
		t.Fatalf("searched skills.sh %d times, want once for each skill without a source record", got)
	}
	// 两个技能搜到同一个来源,只打开一次。
	if got := s.requested("api.github.com/repos/owner/repo/commits/main"); got != 1 {
		t.Fatalf("opened owner/repo %d times", got)
	}
	// 不改库、不改磁盘。
	if skills, err := f.lib.ListSkills(ctx); err != nil || len(skills) != 0 {
		t.Fatalf("skills = %v, %v", skills, err)
	}
	if after := listUnmanagedSkills(t, f.lib); !reflect.DeepEqual(after, unmanaged) {
		t.Fatalf("unmanaged = %#v, want %#v", after, unmanaged)
	}

	// 按搜到的来源整批入库:pdf 是来源里的那一版,docx 说不清是不是改过。
	results := fetchUnmanagedSkills(t, f.lib, library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{
		{Folders: []library.FolderInScope{{Scope: f.claude, Name: "pdf"}}, SourceInput: found[1].SourceInput},
		{Folders: []library.FolderInScope{{Scope: f.claude, Name: "docx"}}, SourceInput: found[0].SourceInput},
	}})
	pdf, docx := results[0].Skill, results[1].Skill
	if pdf.CollectionID != docx.CollectionID || pdf.Revision == "" || docx.Revision != "" {
		t.Fatalf("pdf = %#v, docx = %#v", pdf, docx)
	}
	if version := f.currentVersion(t, pdf); version.MaybeEdited {
		t.Fatalf("pdf version = %#v, want the source's version", version)
	}
	if version := f.currentVersion(t, docx); !version.MaybeEdited {
		t.Fatalf("docx version = %#v, want maybe edited", version)
	}
}

func TestFindUnmanagedSourcesTakesTheMostSimilarSourceAboveTheThreshold(t *testing.T) {
	isolateGit(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	s := serveSearchedSite(t, nil)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	directory := scopeDirectory(t, lib, claude)
	body := "one\ntwo\nthree\nfour\nfive\nsix"
	writeFile(t, filepath.Join(directory, "alpha", "SKILL.md"), []byte(skillDocument("alpha", body)), 0o644)
	// 文件夹名和技能名不同:按 SKILL.md 里的 name 搜。
	writeFile(t, filepath.Join(directory, "Beta-Folder", "SKILL.md"), []byte(skillDocument("beta", body)), 0o644)
	writeFile(t, filepath.Join(directory, "gamma", "SKILL.md"), []byte(skillDocument("gamma", body)), 0o644)
	writeFile(t, filepath.Join(directory, "delta", "SKILL.md"), []byte(skillDocument("delta", body)), 0o644)
	// alpha:near 只差一行、排在前面,same 一模一样,取得分高的 same。
	s.searchResults(t, "alpha", [2]string{"alpha", "near.example"}, [2]string{"alpha", "same.example"})
	// beta:far 的非空行只有五成多重合,不到六成,认不出来源。
	s.searchResults(t, "beta", [2]string{"beta", "far.example"})
	// gamma:来源连不上;delta:skills.sh 连不上。
	s.searchResults(t, "gamma", [2]string{"gamma", "down.example"})
	s.fail("https://skills.sh/api/search?"+url.Values{"q": {"delta"}, "limit": {"20"}}.Encode(), http.StatusInternalServerError)
	s.fail("https://down.example/.well-known/agent-skills/index.json", http.StatusInternalServerError)
	artifactIndex(t, s.site, "https://near.example", []artifact{{name: "alpha", kind: "skill-md", content: []byte(skillDocument("alpha", "one\ntwo\nthree\nfour\nfive\nother"))}})
	artifactIndex(t, s.site, "https://same.example", []artifact{{name: "alpha", kind: "skill-md", content: []byte(skillDocument("alpha", body))}})
	artifactIndex(t, s.site, "https://far.example", []artifact{{name: "beta", kind: "skill-md", content: []byte(skillDocument("beta", "one\ntwo\nx\ny\nz\nw"))}})

	found := findUnmanagedSources(t, lib,
		library.FolderInScope{Scope: claude, Name: "alpha"},
		library.FolderInScope{Scope: claude, Name: "Beta-Folder"},
		library.FolderInScope{Scope: claude, Name: "gamma"},
		library.FolderInScope{Scope: claude, Name: "delta"},
		library.FolderInScope{Scope: claude, Name: "alpha"},
		library.FolderInScope{Scope: claude, Name: "missing"},
	)
	want := []library.FoundUnmanagedSource{{Scope: claude, Name: "alpha", SourceInput: "https://same.example"}}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("found = %#v, want %#v", found, want)
	}
	if got := s.requested("skills.sh/api/search"); got != 4 {
		t.Fatalf("searched skills.sh %d times, want once for each name", got)
	}

	// 搜到的来源和用户给的链接一样能入库;内容一致时就是来源里的那一版。
	result, err := lib.FetchUnmanaged(t.Context(), library.FetchUnmanagedRequest{Scope: claude, Name: "alpha", SourceInput: found[0].SourceInput})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill.Source.Type != library.SourceWellKnown || result.Skill.Source.Label != "same.example" || result.Skill.Revision == "" {
		t.Fatalf("fetched = %#v", result.Skill)
	}
}

func TestFindUnmanagedSourcesRunsAtMostFourRequestsAndOpensEachSourceOnce(t *testing.T) {
	isolateGit(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	s := serveSearchedSite(t, nil)
	s.delay = 20 * time.Millisecond
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	var folders []library.FolderInScope
	var artifacts []artifact
	for _, name := range strings.Fields("a1 a2 a3 a4 a5 a6 a7 a8 a9 a10") {
		content := skillDocument(name, "same body")
		for _, scope := range []library.Scope{claude, codex} {
			writeFile(t, filepath.Join(scopeDirectory(t, lib, scope), name, "SKILL.md"), []byte(content), 0o644)
			folders = append(folders, library.FolderInScope{Scope: scope, Name: name})
		}
		s.searchResults(t, name, [2]string{name, "docs.example"})
		artifacts = append(artifacts, artifact{name: name, kind: "skill-md", content: []byte(content)})
	}
	artifactIndex(t, s.site, "https://docs.example", artifacts)

	found := findUnmanagedSources(t, lib, folders...)
	if len(found) != len(folders) {
		t.Fatalf("found = %#v", found)
	}
	for i, each := range found {
		if each.Scope != folders[i].Scope || each.Name != folders[i].Name || each.SourceInput != "https://docs.example" {
			t.Fatalf("found[%d] = %#v", i, each)
		}
	}
	if peak := s.peak.Load(); peak > 4 || peak < 2 {
		t.Fatalf("peak parallel requests = %d, want 2 to 4", peak)
	}
	// 同名同内容的几处只搜一次;十个技能共用一个来源,只打开一次。
	if got := s.requested("skills.sh/api/search"); got != 10 {
		t.Fatalf("searched skills.sh %d times", got)
	}
	if got := s.requested("docs.example"); got != 1 {
		t.Fatalf("opened docs.example %d times", got)
	}
	if entries, err := os.ReadDir(scopeDirectory(t, lib, claude)); err != nil || len(entries) != 10 {
		t.Fatalf("claude directory = %v, %v", entries, err)
	}
}

func TestFindUnmanagedSourcesErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	if found, err := lib.FindUnmanagedSources(t.Context(), library.FindUnmanagedSourcesRequest{}); err != nil || found == nil || len(found) != 0 {
		t.Fatalf("empty request = %#v, %v", found, err)
	}
	_, err := lib.FindUnmanagedSources(t.Context(), library.FindUnmanagedSourcesRequest{Folders: []library.FolderInScope{{Scope: claude, Name: "../x"}}})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "name"})
	_, err = lib.FindUnmanagedSources(t.Context(), library.FindUnmanagedSourcesRequest{Folders: []library.FolderInScope{{Scope: library.ProjectScope(library.AgentClaudeCode, 99), Name: "x"}}})
	requireLibraryError(t, err, library.NotFound)
}
