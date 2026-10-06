package library_test

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

var lowercaseGitSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)

func TestGitHubFetchRecordsEachSkillDirectoryTreeSHA(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte(skillDocument("first", "first body")), 0o644)
	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", "second body")), 0o644)
	commitGit(t, repository, "initial")

	lib := openTestLibrary(t)
	ctx := t.Context()
	serveGitHubCloneOnly(t, repository)
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := getFetchedSkills(ctx, lib, preview, candidateNames(preview))
	if err != nil || len(fetched) != 2 {
		t.Fatalf("Fetch = %v, %v", fetched, err)
	}

	want := map[string]string{
		"first":  gitRevParse(t, repository, "HEAD:first"),
		"second": gitRevParse(t, repository, "HEAD:second"),
	}
	if want["first"] == want["second"] {
		t.Fatal("fixture skills unexpectedly share a tree SHA")
	}
	seen := map[string]string{}
	for _, skill := range fetched {
		got := readPersistedGitTreeSHA(t, lib, skill.ID)
		if got != want[skill.Name] {
			t.Fatalf("%s GitTreeSHA = %q, want %q", skill.Name, got, want[skill.Name])
		}
		seen[skill.Name] = got
	}
	if seen["first"] == seen["second"] {
		t.Fatalf("skills reused GitTreeSHA %q", seen["first"])
	}
}

func TestGitHubRootSkillUsesCommitRootTreeSHA(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "SKILL.md"), []byte(skillDocument("root-skill", "root")), 0o644)
	writeFile(t, filepath.Join(repository, "notes.md"), []byte("extra\n"), 0o644)
	commitGit(t, repository, "initial")
	want := gitRevParse(t, repository, "HEAD^{tree}")

	lib := openTestLibrary(t)
	ctx := t.Context()
	serveGitHubCloneOnly(t, repository)
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 1 {
		t.Fatalf("Fetch = %v, %v", fetched, err)
	}
	if got := readPersistedGitTreeSHA(t, lib, fetched[0].ID); got != want {
		t.Fatalf("root GitTreeSHA = %q, want commit root tree %q", got, want)
	}
}

// readPersistedGitTreeSHA 读当前版本来源读取记录里 GitHub 记下的目录 tree SHA。
// 这是 GitHub 来源的内部记录,公开方法不暴露,只能直读库文件核对;未记录时为空。
func readPersistedGitTreeSHA(t *testing.T, lib *library.Library, id library.SkillID) string {
	t.Helper()
	sha := readLibraryScalar(t, testLibraryPath(t, lib), `
		SELECT CASE WHEN v.source_state = '' THEN '' ELSE COALESCE(json_extract(v.source_state, '$.tree'), '') END
		FROM skills s JOIN skill_versions v ON v.id = s.current_version_id
		WHERE s.id = ?
	`, id)
	if sha != "" && !lowercaseGitSHA.MatchString(sha) {
		t.Fatalf("GitTreeSHA = %q, want 40-char lowercase hex", sha)
	}
	return sha
}

func gitRevParse(t *testing.T, repository, spec string) string {
	t.Helper()
	output, err := exec.Command("git", "-c", "core.excludesFile=/dev/null", "-C", repository, "rev-parse", spec).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", spec, err)
	}
	sha := strings.ToLower(strings.TrimSpace(string(output)))
	if !lowercaseGitSHA.MatchString(sha) {
		t.Fatalf("git rev-parse %s = %q, want 40-char lowercase hex", spec, sha)
	}
	return sha
}
