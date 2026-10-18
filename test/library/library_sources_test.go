package library_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// sourceFixture 是一种来源的测试替身。publish 把来源里的技能整个换成 skills(技能名 → SKILL.md 正文),
// 没列出的技能从来源里删掉;request 是对这个来源做入库预览的请求。
type sourceFixture struct {
	request library.PreviewFetchRequest
	publish func(skills map[string]string)
}

// sourceKinds 列出每种来源的每条读取路径。新增一种来源时在这里加一行,让它走一遍
// TestEverySourceGoesThroughTheUpdateLifecycle。
var sourceKinds = []struct {
	name  string
	serve func(t *testing.T) sourceFixture
}{
	{"local directory", func(t *testing.T) sourceFixture {
		dir := t.TempDir()
		return sourceFixture{
			request: localRequest(dir),
			publish: func(skills map[string]string) { writeSkillTree(t, dir, skills) },
		}
	}},
	{"git repository", func(t *testing.T) sourceFixture {
		config := isolateGit(t)
		repo := t.TempDir()
		runGit(t, repo, "init", "-b", "main")
		redirectRemote(t, config, repo, "https://git.example.com/team/skills.git")
		return sourceFixture{
			request: library.PreviewFetchRequest{Input: "https://git.example.com/team/skills.git"},
			publish: func(skills map[string]string) {
				writeSkillTree(t, repo, skills)
				commitGit(t, repo, "publish")
			},
		}
	}},
	{"github api", func(t *testing.T) sourceFixture {
		repo := t.TempDir()
		runGit(t, repo, "init", "-b", "main")
		var api *gitHubAPIMock
		return sourceFixture{
			request: gitHubRequest(),
			publish: func(skills map[string]string) {
				writeSkillTree(t, repo, skills)
				commitGit(t, repo, "publish")
				if api == nil {
					api = serveGitHubAPI(t, inspectGitHubRepo(t, repo))
					blockGitHubClone(t)
					return
				}
				api.Swap(inspectGitHubRepo(t, repo))
			},
		}
	}},
	{"github clone", func(t *testing.T) sourceFixture {
		repo := t.TempDir()
		runGit(t, repo, "init", "-b", "main")
		return sourceFixture{
			request: gitHubCloneRequest(t, repo),
			publish: func(skills map[string]string) {
				writeSkillTree(t, repo, skills)
				commitGit(t, repo, "publish")
			},
		}
	}},
	{"direct download", func(t *testing.T) sourceFixture {
		server := newArchiveServer(t, nil, "")
		version := 0
		return sourceFixture{
			request: server.request(),
			publish: func(skills map[string]string) {
				files := map[string]string{}
				for name, body := range skills {
					files["skills/"+name+"/SKILL.md"] = skillDocument(name, body)
				}
				version++
				server.serve(zipArchive(t, files), fmt.Sprintf(`"v%d"`, version))
			},
		}
	}},
	{"well-known skills index", func(t *testing.T) sourceFixture {
		isolateGit(t)
		s := serveSite(t)
		return sourceFixture{
			request: library.PreviewFetchRequest{Input: "https://docs.example"},
			publish: func(skills map[string]string) {
				index := map[string]map[string]string{}
				for name, body := range skills {
					index[name] = map[string]string{"SKILL.md": skillDocument(name, body)}
				}
				legacyIndex(t, s, "https://docs.example", index)
			},
		}
	}},
	{"well-known agent-skills index", func(t *testing.T) sourceFixture {
		isolateGit(t)
		s := serveSite(t)
		return sourceFixture{
			request: library.PreviewFetchRequest{Input: "https://docs.example"},
			publish: func(skills map[string]string) {
				var artifacts []artifact
				for _, name := range slices.Sorted(maps.Keys(skills)) {
					artifacts = append(artifacts, artifact{name: name, kind: "skill-md", content: []byte(skillDocument(name, skills[name]))})
				}
				artifactIndex(t, s, "https://docs.example", artifacts)
			},
		}
	}},
}

// writeSkillTree 把 dir/skills 换成 skills 里的技能,每个一个 SKILL.md。
func writeSkillTree(t *testing.T, dir string, skills map[string]string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(dir, "skills")); err != nil {
		t.Fatal(err)
	}
	for name, body := range skills {
		writeFile(t, filepath.Join(dir, "skills", name, "SKILL.md"), []byte(skillDocument(name, body)), 0o644)
	}
}

// 每种来源都走同一遍:入库、没变化、改了一个、更新、出现新技能、删了一个。结论只经公开方法读取,
// 和来源怎么判断有没有变化(比版本号、比内容摘要还是取文件比较)无关。
func TestEverySourceGoesThroughTheUpdateLifecycle(t *testing.T) {
	for _, kind := range sourceKinds {
		t.Run(kind.name, func(t *testing.T) {
			src := kind.serve(t)
			src.publish(map[string]string{"alpha": "alpha one", "beta": "beta one"})
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), src.request)
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Sorted(slices.Values(candidateNames(preview))); !slices.Equal(got, []string{"alpha", "beta"}) {
				t.Fatalf("candidates = %v", got)
			}
			fetched := fetchSkills(t, lib, preview, []string{"alpha", "beta"})
			alpha := fetched[slices.IndexFunc(fetched, func(skill library.Skill) bool { return skill.Name == "alpha" })]
			requireSkillFiles(t, lib, alpha.ID, "alpha one")
			check := func(want map[string]library.UpdateCheckStatus) {
				t.Helper()
				checks, err := lib.CheckUpdates(t.Context(), []library.CollectionID{alpha.CollectionID})
				if err != nil || len(checks) != 1 || checks[0].Error != nil {
					t.Fatalf("CheckUpdates = %#v, %v", checks, err)
				}
				got := map[string]library.UpdateCheckStatus{}
				for _, skill := range checks[0].Skills {
					got[skill.Name] = skill.LastUpdateCheck.Status
				}
				if !maps.Equal(got, want) {
					t.Fatalf("check = %v; want %v", got, want)
				}
			}
			check(map[string]library.UpdateCheckStatus{"alpha": library.UpdateCurrent, "beta": library.UpdateCurrent})

			src.publish(map[string]string{"alpha": "alpha two", "beta": "beta one"})
			check(map[string]library.UpdateCheckStatus{"alpha": library.UpdateAvailable, "beta": library.UpdateCurrent})
			if updated, err := updateSkill(t, lib, t.Context(), alpha.ID); err != nil || !updated.Changed {
				t.Fatalf("Update(alpha) = %#v, %v", updated, err)
			}
			requireSkillFiles(t, lib, alpha.ID, "alpha two")
			check(map[string]library.UpdateCheckStatus{"alpha": library.UpdateCurrent, "beta": library.UpdateCurrent})

			src.publish(map[string]string{"alpha": "alpha two", "beta": "beta one", "gamma": "gamma one"})
			check(map[string]library.UpdateCheckStatus{"alpha": library.UpdateCurrent, "beta": library.UpdateCurrent})
			requireNewSkills(t, lib, alpha.CollectionID, []string{"gamma"})

			src.publish(map[string]string{"alpha": "alpha two", "gamma": "gamma one"})
			check(map[string]library.UpdateCheckStatus{"alpha": library.UpdateCurrent, "beta": library.UpdateDeleted})
		})
	}
}
