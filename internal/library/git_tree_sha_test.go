package library_test

import (
	"net/http"
	"net/http/httptest"
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
	preview, err := lib.PreviewSource(ctx, library.SourceSpec{
		Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.FetchFromPreview(ctx, preview.ID, nil)
	if err != nil || len(fetched) != 2 {
		t.Fatalf("FetchFromPreview = %v, %v", fetched, err)
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
		if skill.GitTreeSHA != got {
			t.Fatalf("FetchFromPreview %s GitTreeSHA = %q, want %q", skill.Name, skill.GitTreeSHA, got)
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
	preview, err := lib.PreviewSource(ctx, library.SourceSpec{
		Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.FetchFromPreview(ctx, preview.ID, nil)
	if err != nil || len(fetched) != 1 {
		t.Fatalf("FetchFromPreview = %v, %v", fetched, err)
	}
	if got := readPersistedGitTreeSHA(t, lib, fetched[0].ID); got != want {
		t.Fatalf("root GitTreeSHA = %q, want commit root tree %q", got, want)
	}
}

func TestNonGitFetchesKeepEmptyGitTreeSHA(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "demo")})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	t.Setenv("SKILLS_STORE_URL", server.URL)

	lib := openTestLibrary(t)
	ctx := t.Context()
	localDir := filepath.Join(t.TempDir(), "local-skill")
	writeFile(t, filepath.Join(localDir, "SKILL.md"), []byte(skillDocument("local-skill", "local")), 0o644)
	local, err := fetchLocalDir(ctx, lib, localDir)
	if err != nil {
		t.Fatal(err)
	}

	urlPreview, err := lib.PreviewSource(ctx, library.SourceSpec{
		Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL + "/skills.zip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	urlSkills, err := lib.FetchFromPreview(ctx, urlPreview.ID, []string{"demo"})
	if err != nil || len(urlSkills) != 1 {
		t.Fatalf("URL FetchFromPreview = %v, %v", urlSkills, err)
	}

	storePreview, err := lib.PreviewSource(ctx, library.SourceSpec{
		Type: library.SourceStore, Locator: library.SourceLocator{PackageID: "owner/package", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	storeSkills, err := lib.FetchFromPreview(ctx, storePreview.ID, []string{"demo"})
	if err != nil || len(storeSkills) != 1 {
		t.Fatalf("Store FetchFromPreview = %v, %v", storeSkills, err)
	}

	for _, skill := range []library.Skill{local, urlSkills[0], storeSkills[0]} {
		if got := readPersistedGitTreeSHA(t, lib, skill.ID); got != "" {
			t.Fatalf("%s GitTreeSHA = %q, want empty", skill.Name, got)
		}
		if skill.GitTreeSHA != "" {
			t.Fatalf("fetch result %s GitTreeSHA = %q, want empty", skill.Name, skill.GitTreeSHA)
		}
	}
}

func readPersistedGitTreeSHA(t *testing.T, lib library.Library, id library.SkillID) string {
	t.Helper()
	ctx := t.Context()
	skill, err := lib.GetSkill(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := lib.ListVersions(ctx, id)
	if err != nil || len(versions) == 0 {
		t.Fatalf("ListVersions = %#v, %v", versions, err)
	}
	version, err := lib.GetVersion(ctx, versions[len(versions)-1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if skill.GitTreeSHA != version.GitTreeSHA {
		t.Fatalf("GetSkill GitTreeSHA = %q, GetVersion GitTreeSHA = %q", skill.GitTreeSHA, version.GitTreeSHA)
	}
	if skill.GitTreeSHA != "" && !lowercaseGitSHA.MatchString(skill.GitTreeSHA) {
		t.Fatalf("GitTreeSHA = %q, want 40-char lowercase hex", skill.GitTreeSHA)
	}
	return skill.GitTreeSHA
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
