package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 新技能

func TestNewSkillsAreOnlyThoseAppearingAfterFetch(t *testing.T) {
	for _, mode := range gitHubModes {
		for _, entry := range []struct {
			name    string
			request func() library.PreviewFetchRequest
		}{
			{"skills.sh", func() library.PreviewFetchRequest {
				return library.PreviewFetchRequest{Input: "owner/repo@first", Ref: "main"}
			}},
			{"subpath", func() library.PreviewFetchRequest {
				return library.PreviewFetchRequest{Input: "owner/repo", Ref: "main", Subpath: "skills"}
			}},
		} {
			t.Run(mode.name+"/"+entry.name, func(t *testing.T) {
				ctx := t.Context()
				repo := writeGitHubRepo(t, map[string]repoFile{
					"skills/first/SKILL.md":  {content: []byte(skillDocument("first", "v1")), mode: 0o644},
					"skills/second/SKILL.md": {content: []byte(skillDocument("second", "v1")), mode: 0o644},
					"other/SKILL.md":         {content: []byte(skillDocument("other", "v1")), mode: 0o644},
				})
				advance := mode.serve(t, repo)
				path := filepath.Join(t.TempDir(), "library.db")
				lib := openTestLibraryAt(t, path)
				preview, err := lib.PreviewFetch(ctx, entry.request())
				if err != nil {
					t.Fatal(err)
				}
				first := fetchSkills(t, lib, preview, []string{"first"})[0]
				// 仓库里原有、入库时没选的技能不算新技能。
				writeFile(t, filepath.Join(repo, "skills", "first", "notes.md"), []byte("change"), 0o644)
				commitGit(t, repo, "change first")
				advance()
				checkedSkill(t, lib, first.ID)
				requireNewSkills(t, lib, first.CollectionID, nil)

				writeFile(t, filepath.Join(repo, "skills", "third", "SKILL.md"), []byte(skillDocument("third", "v1")), 0o644)
				commitGit(t, repo, "add third")
				advance()
				if check := checkedSkill(t, lib, first.ID); !check.Pending {
					t.Fatalf("first = %#v; want pending for its own change only", check)
				}
				requireNewSkills(t, lib, first.CollectionID, []string{"third"})
				if err := lib.Close(); err != nil {
					t.Fatal(err)
				}
				lib = openTestLibraryAt(t, path)
				requireNewSkills(t, lib, first.CollectionID, []string{"third"})
				requireCandidateFlags(t, lib, first.CollectionID, map[string]bool{"first": false, "second": false, "third": true, "other": false})

				if err := lib.MarkNotInterested(ctx, first.CollectionID, []string{"third"}); err != nil {
					t.Fatal(err)
				}
				requireNewSkills(t, lib, first.CollectionID, nil)
				requireCandidateFlags(t, lib, first.CollectionID, map[string]bool{"first": false, "second": false, "third": false, "other": false})
			})
		}
	}
}

// requireCandidateFlags 对集合再做入库预览,断言每个候选是否标为新技能。
func requireCandidateFlags(t *testing.T, lib *library.Library, id library.CollectionID, want map[string]bool) {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Collection: id})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, candidate := range preview.Candidates {
		got[candidate.Name] = candidate.New
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates new = %v; want %v", got, want)
	}
}
