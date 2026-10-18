package library_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func hangingGitServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestFetchesRegularSkillWhenRepositoryDeclaresLFSWithoutGitLFS(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "demo", "SKILL.md"), []byte(skillDocument("demo", "plain skill")), 0o644)
	writeFile(t, filepath.Join(repository, ".gitattributes"), []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o644)
	writeFile(t, filepath.Join(repository, "blobs", "data.bin"), []byte("version https://git-lfs.github.com/spec/v1\noid sha256:88d4266fd4e6338d13b845fcf289579d209c897823b9217da3e161936f031589\nsize 4\n"), 0o644)
	lfsArgs := []string{"-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "-c", "filter.lfs.required=false"}
	runGit(t, repository, append(lfsArgs, "add", ".")...)
	runGit(t, repository, append(lfsArgs, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "lfs")...)

	marker := filepath.Join(t.TempDir(), "lfs-invoked")
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "git-lfs"), []byte("#!/bin/sh\necho invoked > \"$GIT_LFS_MARKER\"\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_LFS_MARKER", marker)

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubCloneRequest(t, repository))
	if err != nil {
		t.Fatalf("PreviewFetch with LFS attributes and no git-lfs: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git-lfs was invoked during PreviewFetch")
	}
	fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"demo"})
	if err != nil || len(fetched) != 1 || fetched[0].Name != "demo" {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	if got := string(skillFiles(t, lib, fetched[0])[0].Content); !strings.Contains(got, "plain skill") {
		t.Fatalf("fetched content = %q", got)
	}
}

func TestPreviewRejectsExtGitProtocol(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "pwned")
	// 公开输入只接受 github.com;用户 git 配置仍可把它改写成 ext 协议,Library 必须拒绝执行。
	serveGitHubRemoteOnly(t, "ext::touch "+sentinel)
	runGit(t, t.TempDir(), "config", "--file", os.Getenv("GIT_CONFIG_GLOBAL"), "protocol.ext.allow", "always")

	lib := openTestLibrary(t)
	_, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("ext protocol executed a host command")
	}
	if err == nil {
		t.Fatal("PreviewFetch accepted ext protocol")
	}
}

func TestPreviewGitTimesOutWithoutCallerDeadline(t *testing.T) {
	t.Setenv("SKILLS_GIT_TIMEOUT", "1s")
	lib := openTestLibrary(t)
	start := time.Now()
	serveGitHubRemoteOnly(t, hangingGitServer(t))
	_, err := lib.PreviewFetch(context.Background(), gitHubRequest())
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("PreviewFetch hung for %v without independent git timeout", elapsed)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("timeout leaked caller context error: %v", err)
	}
	if !library.IsCode(err, library.SourceUnreachable) || !strings.Contains(strings.ToLower(err.Error()), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestCheckUpdatesAndUpdateGitTimeOutWithoutCallerDeadline(t *testing.T) {
	repository, _ := githubRemote(t)
	t.Setenv("SKILLS_GIT_TIMEOUT", "1s")
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	remote := hangingGitServer(t)
	config := os.Getenv("GIT_CONFIG_GLOBAL")
	runGit(t, repository, "config", "--file", config, "--unset-all", "url.file://"+repository+".insteadOf")
	runGit(t, repository, "config", "--file", config, "url."+remote+"/.insteadOf", "https://github.com/")
	runGit(t, repository, "config", "--file", config, "--add", "url."+remote+"/.insteadOf", "git@github.com:")

	for _, name := range []string{"CheckUpdates", "Update"} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			var err error
			if name == "CheckUpdates" {
				_, err = checkSkillUpdate(t, lib, t.Context(), fetched[0].ID)
			} else {
				_, err = updateSkill(t, lib, t.Context(), fetched[0].ID)
			}
			elapsed := time.Since(start)
			if elapsed > 10*time.Second {
				t.Fatalf("%s hung for %v without independent git timeout", name, elapsed)
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				t.Fatalf("timeout leaked caller context error: %v", err)
			}
			if !library.IsCode(err, library.SourceUnreachable) || !strings.Contains(strings.ToLower(err.Error()), "timed out") {
				t.Fatalf("timeout error = %v", err)
			}
		})
	}
}

func TestPreviewGitCancelStopsImmediately(t *testing.T) {
	lib := openTestLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	serveGitHubRemoteOnly(t, server.URL)
	start := time.Now()
	_, err := lib.PreviewFetch(ctx, gitHubRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancel = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("cancel did not stop git")
	}
}

func TestPreviewGitDistinguishesFailureReasonsWithoutExposingCredentials(t *testing.T) {
	repository, _ := githubRemote(t)
	lib := openTestLibrary(t)
	secret := "secret-token-value"

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(forbidden.Close)
	authURL := strings.Replace(forbidden.URL, "://", "://user:"+secret+"@", 1)

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()

	for _, tc := range []struct {
		name    string
		remote  string
		ref     string
		code    library.ErrorCode
		message string
	}{
		{
			name:    "authentication",
			remote:  authURL,
			ref:     "trunk",
			code:    library.Permission,
			message: "authentication failed",
		},
		{
			name:    "missing ref",
			remote:  "file://" + repository,
			ref:     "does-not-exist",
			code:    library.NotFound,
			message: "ref not found",
		},
		{
			name:    "unreachable",
			remote:  closed.URL,
			ref:     "trunk",
			code:    library.SourceUnreachable,
			message: "could not be reached",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveGitHubRemoteOnly(t, tc.remote)
			_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo", Ref: tc.ref})
			if !library.IsCode(err, tc.code) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v, want %s containing %q", err, tc.code, tc.message)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposed credentials: %v", err)
			}
		})
	}
}
