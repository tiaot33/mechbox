package library_test

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// skillRepository 建一个 main 分支的仓库:根目录本身是技能 root,skills 下另有 alpha 与 beta。
// 从根目录发现只得到 root,从 skills 目录发现得到 alpha 与 beta。
func skillRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "SKILL.md"), []byte(skillDocument("root", "first")), 0o644)
	for _, dir := range []string{"skills/alpha", "skills/beta"} {
		writeFile(t, filepath.Join(repository, dir, "SKILL.md"), []byte(skillDocument(filepath.Base(dir), "first")), 0o644)
	}
	commitGit(t, repository, "initial")
	return repository
}

// redirectRemote 让 git 把 remote 改写到本地仓库 repository。
func redirectRemote(t *testing.T, config, repository, remote string) {
	t.Helper()
	runGit(t, repository, "config", "--file", config, "--add", "url.file://"+repository+".insteadOf", remote)
}

func TestGitLabTreeLinkSplitsBranchAndSubpath(t *testing.T) {
	config := isolateGit(t)
	repository := skillRepository(t)
	redirectRemote(t, config, repository, "https://gitlab.com/group/subgroup/repo.git")
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "gitlab.com/group/subgroup/repo/-/tree/main/skills"})
	if err != nil {
		t.Fatal(err)
	}
	want := library.Source{Type: library.SourceGit, Label: "gitlab.com/group/subgroup/repo", Ref: "main", ViewURL: "https://gitlab.com/group/subgroup/repo/-/tree/main"}
	if preview.Source != want {
		t.Fatalf("source = %#v; want %#v", preview.Source, want)
	}
	if want := []library.Candidate{{Name: "alpha", Path: "skills/alpha"}, {Name: "beta", Path: "skills/beta"}}; !reflect.DeepEqual(preview.Candidates, want) {
		t.Fatalf("candidates = %#v; want only skills under the subpath %#v", preview.Candidates, want)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha"})
	// gitlab: 简写与不带路径的链接指向同一个仓库与分支,是同一个集合。
	for _, input := range []string{"gitlab:group/subgroup/repo", "https://gitlab.com/group/subgroup/repo"} {
		again, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: input, Subpath: "skills"})
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if again.Source != want || len(again.Candidates) != 2 || again.Candidates[0].Fetched != fetched[0].ID {
			t.Fatalf("%s = %#v", input, again)
		}
	}
}

func TestPrivateGitLabRepositoryUsesConfiguredSSHKey(t *testing.T) {
	isolateGit(t)
	repository := skillRepository(t)
	wrapper := filepath.Join(t.TempDir(), "ssh")
	writeFile(t, wrapper, []byte("#!/bin/sh\ncase \" $* \" in *' -oBatchMode=yes '*) ;; *) exit 9 ;; esac\ncase \" $* \" in *' git@gitlab.com '*) ;; *) exit 11 ;; esac\nexec git-upload-pack \"$TEST_SSH_REPOSITORY\"\n"), 0o755)
	t.Setenv("GIT_SSH_COMMAND", "'"+wrapper+"'")
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	t.Setenv("TEST_SSH_REPOSITORY", repository)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "git@gitlab.com:group/private.git", Subpath: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	want := library.Source{Type: library.SourceGit, Label: "gitlab.com/group/private", Ref: "main", ViewURL: "https://gitlab.com/group/private/-/tree/main"}
	if preview.Source != want || len(preview.Candidates) != 2 {
		t.Fatalf("preview = %#v", preview)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha", "beta"})
	if len(fetched) != 2 || fetched[0].Source != want {
		t.Fatalf("fetched = %#v", fetched)
	}
}

func TestGitHostLinkWithoutDotGitIsRecognizedAsGitRepository(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	runGit(t, root, "clone", "--quiet", "--bare", skillRepository(t), filepath.Join(root, "org", "repo"))
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{Path: filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend"), Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/info/refs"), strings.HasSuffix(r.URL.Path, "/git-upload-pack"):
			backend.ServeHTTP(w, r)
		case r.URL.Path == "/org/repo":
			// 像 GitHub Enterprise 的仓库网页:地址栏里的地址打开是一个 HTML 页面。
			_, _ = w.Write([]byte("<html>org/repo</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: server.URL + "/org/repo"})
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(server.URL, "http://")
	if want := (library.Source{Type: library.SourceGit, Label: host + "/org/repo", Ref: "main"}); preview.Source != want {
		t.Fatalf("source = %#v; want %#v", preview.Source, want)
	}
	if got := candidateNames(preview); !reflect.DeepEqual(got, []string{"root"}) {
		t.Fatalf("candidates = %v", got)
	}
	fetched := fetchSkills(t, lib, preview, []string{"root"})
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateCurrent {
		t.Fatalf("check = %s, %v", status, err)
	}
}

func TestGitRepositoryCheckUsesLsRemoteAndPlanListsCommits(t *testing.T) {
	config := isolateGit(t)
	repository := skillRepository(t)
	runGit(t, repository, "rm", "--quiet", "SKILL.md")
	commitGit(t, repository, "Move every skill under skills")
	redirectRemote(t, config, repository, "https://gitlab.com/group/repo.git")
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://gitlab.com/group/repo"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha", "beta"})

	// 藏起仓库的全部对象:git ls-remote 只读引用仍然能用,克隆一定失败。没有新 commit 时检查更新不该克隆。
	objects := filepath.Join(repository, ".git", "objects")
	if err := os.Rename(objects, objects+".hidden"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(objects, 0o755); err != nil {
		t.Fatal(err)
	}
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateCurrent {
		t.Fatalf("check without new commit = %s, %v; want current without cloning", status, err)
	}
	if err := os.RemoveAll(objects); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(objects+".hidden", objects); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(repository, "skills", "alpha", "SKILL.md"), []byte(skillDocument("alpha", "second")), 0o644)
	commitGit(t, repository, "Rewrite alpha\n\nExplain the new steps.")
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateAvailable {
		t.Fatalf("check after commit = %s, %v", status, err)
	}
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[1].ID); err != nil || status != library.UpdateCurrent {
		t.Fatalf("unchanged skill after commit = %s, %v", status, err)
	}
	plan, err := lib.PlanUpdate(t.Context(), fetched[0].CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Commits) != 1 || plan.Commits[0].Message != "Rewrite alpha\n\nExplain the new steps." || plan.Commits[0].Author != "Test" {
		t.Fatalf("plan commits = %#v", plan.Commits)
	}
}

func TestGitSkillWithBrokenSkillFileIsUnreadableNotDeleted(t *testing.T) {
	config := isolateGit(t)
	repository := skillRepository(t)
	runGit(t, repository, "rm", "--quiet", "SKILL.md")
	commitGit(t, repository, "Move every skill under skills")
	redirectRemote(t, config, repository, "https://gitlab.com/group/repo.git")
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://gitlab.com/group/repo"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha", "beta"})
	// alpha 的 SKILL.md 丢了 description:这个技能这次没取到,不算来源里已删除。
	writeFile(t, filepath.Join(repository, "skills", "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\nbody"), 0o644)
	commitGit(t, repository, "Break alpha")
	status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID)
	if status != library.UpdateFailed {
		t.Fatalf("check with broken SKILL.md = %s; want failed", status)
	}
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Name: "alpha"})
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[1].ID); err != nil || status != library.UpdateCurrent {
		t.Fatalf("beta = %s, %v", status, err)
	}
}

func TestDownloadedSkillWithBrokenSkillFileIsUnreadableNotDeleted(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{"alpha/SKILL.md": skillDocument("alpha", "a"), "beta/SKILL.md": skillDocument("beta", "b")}), "")
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), server.request())
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha"})
	server.serve(zipArchive(t, map[string]string{"alpha/SKILL.md": "---\nname: alpha\n---\nbody", "beta/SKILL.md": skillDocument("beta", "b")}), "")
	status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID)
	if status != library.UpdateFailed {
		t.Fatalf("check with broken SKILL.md = %s; want failed", status)
	}
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Name: "alpha"})
}

func TestAnnotatedTagWithoutNewCommitDoesNotClone(t *testing.T) {
	config := isolateGit(t)
	repository := skillRepository(t)
	runGit(t, repository, "-c", "user.name=Test", "-c", "user.email=test@example.com", "tag", "-a", "v1", "-m", "release v1")
	redirectRemote(t, config, repository, "https://gitlab.com/group/repo.git")
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://gitlab.com/group/repo", Ref: "v1"})
	if err != nil || preview.Source.Ref != "v1" {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"root"})
	// 换上一个记下克隆的 git 包装脚本:tag 没动时检查更新只该 ls-remote。
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "cloned")
	writeFile(t, filepath.Join(dir, "git"), []byte("#!/bin/sh\ncase \" $* \" in *' clone '*) touch '"+marker+"' ;; esac\nexec '"+real+"' \"$@\"\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if status, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); err != nil || status != library.UpdateCurrent {
		t.Fatalf("check on unchanged annotated tag = %s, %v", status, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("check on unchanged annotated tag cloned the repository: %v", err)
	}
}
