package library_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

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
		// 索引里列出的系统文件不进技能库。
		"alpha": {"SKILL.md": describedSkill("alpha", "Alpha", "a"), "references/guide.md": "guide", "references/.DS_Store": "finder"},
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
