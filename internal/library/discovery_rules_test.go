package library_test

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

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
		if api != nil {
			assertGitHubPreviewRequests(t, api, fx, "skills/category/useful/SKILL.md", ".claude/skills/claude/SKILL.md", ".codex/skills/codex/SKILL.md")
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
				fetched, err := getFetchedSkills(t.Context(), lib, got, candidateNames(got))
				if err != nil || len(fetched) != len(tc.paths) {
					t.Fatalf("fetch = %#v, %v", fetched, err)
				}
				for i, skill := range fetched {
					prefix := tc.paths[i] + "/"
					if tc.paths[i] == "." {
						prefix = ""
					}
					for _, file := range skill.Files {
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
		if api != nil {
			assertGitHubPreviewRequests(t, api, fx, ".claude-plugin/marketplace.json", ".claude-plugin/plugin.json", "plugins/a/skills/x/skill.md", "plugins/a/skills/y/SKILL.md", "plugins/a/special/q/SKILL.md", "extras/solo/SKILL.md")
		}
	})
}

func TestGitHubUpdateUsesDiscoveredCandidates(t *testing.T) {
	for _, source := range []string{"github", "clone"} {
		t.Run(source, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"skills/first/SKILL.md":  {content: []byte(skillDocument("same", "old")), mode: 0o644},
				"skills/second/SKILL.md": {content: []byte(skillDocument("same", "second")), mode: 0o644},
				"skills/keeper/SKILL.md": {content: []byte(skillDocument("keeper", "keep")), mode: 0o644},
			})
			fx := inspectGitHubRepo(t, repo)
			spec := gitHubRequest()
			var api *gitHubAPIMock
			if source == "github" {
				api = serveGitHubAPI(t, fx)
				blockGitHubClone(t)
			} else {
				serveGitHubCloneOnly(t, repo)
			}
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"same"})
			if err != nil {
				t.Fatal(err)
			}
			id := fetched[0].ID
			writeFile(t, filepath.Join(repo, "skills/first/SKILL.md"), []byte(skillDocument("same", "changed")), 0o644)
			commitGit(t, repo, "change first")
			fx = inspectGitHubRepo(t, repo)
			if api != nil {
				api.Swap(fx)
			}
			available, err := checkSingleUpdate(t, lib, t.Context(), id)
			if err != nil || !available {
				t.Fatalf("check = %v, %v", available, err)
			}
			updated, err := lib.Update(t.Context(), id)
			if err != nil || !updated.Changed || readPersistedGitTreeSHA(t, lib, updated.Skill.ID) != fx.treeSHA("skills/first") || string(updated.Skill.Files[0].Content) != skillDocument("same", "changed") {
				t.Fatalf("update = %#v, %v", updated, err)
			}
			runGit(t, repo, "mv", "skills", "examples")
			writeFile(t, filepath.Join(repo, "skills/other/SKILL.md"), []byte(skillDocument("other", "keep conventional")), 0o644)
			commitGit(t, repo, "move out of discovery")
			if api != nil {
				api.Swap(inspectGitHubRepo(t, repo))
			}
			if _, err := lib.Update(t.Context(), id); !library.IsCode(err, library.NotFound) {
				t.Fatalf("missing update = %v", err)
			}
			runGit(t, repo, "rm", "-r", "skills", "examples")
			writeFile(t, filepath.Join(repo, "README.md"), []byte("no skills"), 0o644)
			commitGit(t, repo, "remove candidates")
			if api != nil {
				api.Swap(inspectGitHubRepo(t, repo))
			}
			if _, err := checkSingleUpdate(t, lib, t.Context(), id); !library.IsCode(err, library.NotFound) {
				t.Fatalf("empty source = %v", err)
			}
		})
	}
}

func TestGitHubBatchCheckReusesDiscovery(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"skills/first/SKILL.md":  {content: []byte(skillDocument("first", "body")), mode: 0o644},
		"skills/second/SKILL.md": {content: []byte(skillDocument("second", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	// 推进一个与两个 Skill 都无关的 commit,让检查必须取 tree 重新发现候选。
	writeFile(t, filepath.Join(repo, "README.md"), []byte("unrelated"), 0o644)
	commitGit(t, repo, "unrelated")
	api.Swap(inspectGitHubRepo(t, repo))
	api.Reset()
	got, err := lib.CheckUpdates(t.Context(), []library.SkillID{fetched[0].ID, fetched[1].ID})
	if err != nil || len(got) != 2 || got[0].Error != nil || got[1].Error != nil || got[0].Available || got[1].Available {
		t.Fatalf("checks = %#v, %v", got, err)
	}
	// 入库预览已把各候选 SKILL.md 写入进程内 memo;commit 前进只再取新 root tree。
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 1 || trees != 1 || blobs != 0 {
		t.Fatalf("CheckUpdates commits=%d trees=%d blobs=%d, want commit+tree only: %#v", commits, trees, blobs, api.snapshot())
	}
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
		if spec.GitHub == nil {
			return
		}
		spec.GitHub.Subpath = "area"
		got, err := lib.PreviewFetch(t.Context(), spec)
		if err != nil || len(got.Candidates) != 2 || got.Candidates[0].Path != "area/skills/demo" || got.Candidates[1].Path != "area/extras/solo" || got.Candidates[1].PluginName != "subpath" {
			t.Fatalf("subpath = %#v, %v", got, err)
		}
		if api != nil {
			assertGitHubPreviewRequests(t, api, fx, "area/.claude-plugin/plugin.json", "area/skills/demo/SKILL.md", "area/extras/solo/SKILL.md")
		}
		spec.GitHub.Subpath = "area/skills/demo"
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
				fetched, err := getFetchedSkills(t.Context(), lib, got, candidateNames(got))
				if err != nil || len(fetched[0].Files) != len(files) {
					t.Fatalf("case files = %#v, %v", fetched, err)
				}
				for _, file := range fetched[0].Files {
					if string(file.Content) != files["demo/"+file.Path] {
						t.Fatalf("changed filename/content: %#v", file)
					}
				}
			})
		}
	}
}
