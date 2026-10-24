package library_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// gitHubModes 是 GitHub 来源的两种读取方式:REST API,以及 API 不可用时的浅克隆。
// 同一条行为在两种方式下结果应完全一致。serve 返回的 advance 在仓库提交新内容后调用,让远端看到新的 HEAD。
var gitHubModes = []struct {
	name  string
	serve func(t *testing.T, repo string) (advance func())
}{
	{"api", func(t *testing.T, repo string) func() {
		api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
		blockGitHubClone(t)
		return func() { api.Swap(inspectGitHubRepo(t, repo)) }
	}},
	{"clone", func(t *testing.T, repo string) func() {
		serveGitHubCloneOnly(t, repo)
		return func() {}
	}},
}

func TestGitHubPreviewAndFetch(t *testing.T) {
	for _, mode := range gitHubModes {
		t.Run(mode.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"first/SKILL.md":       {content: []byte(skillDocument("first", "first body")), mode: 0o644},
				"first/scripts/run":    {content: []byte{0, 255, 1}, mode: 0o755},
				"first/data.bin":       {content: []byte{0, 1, 2, 255}, mode: 0o644},
				"other-dir/SKILL.md":   {content: []byte(skillDocument("second", "second body")), mode: 0o644},
				"unrelated/large.bin":  {content: []byte("not a skill"), mode: 0o644},
				"other-dir/extra.md":   {content: []byte("other skill"), mode: 0o644},
				"first/nested/note.md": {content: []byte("nested"), mode: 0o644},
				// 系统和缓存文件不进技能库。
				"first/.DS_Store":                       {content: []byte("finder"), mode: 0o644},
				"first/__pycache__/run.cpython-312.pyc": {content: []byte("cache"), mode: 0o644},
			})
			mode.serve(t, repo)
			lib := openTestLibrary(t)

			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			if preview.Source.Ref != "main" || preview.Revision != gitRevParse(t, repo, "HEAD") {
				t.Fatalf("Ref / Revision = %q / %q", preview.Source.Ref, preview.Revision)
			}
			want := []library.Candidate{{Name: "first", Path: "first"}, {Name: "second", Path: "other-dir"}}
			if !reflect.DeepEqual(preview.Candidates, want) {
				t.Fatalf("candidates = %#v, want %#v", preview.Candidates, want)
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("preview wrote Library: %v, %v", skills, err)
			}

			fetched := fetchSkills(t, lib, preview, []string{"first"})
			wantFiles := []library.File{
				{Path: "SKILL.md", Content: []byte(skillDocument("first", "first body")), Mode: library.ModeRegular},
				{Path: "data.bin", Content: []byte{0, 1, 2, 255}, Mode: library.ModeRegular},
				{Path: "nested/note.md", Content: []byte("nested"), Mode: library.ModeRegular},
				{Path: "scripts/run", Content: []byte{0, 255, 1}, Mode: library.ModeExecutable},
			}
			if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), wantFiles) {
				t.Fatalf("files = %#v, want %#v", skillFiles(t, lib, fetched[0]), wantFiles)
			}
			if got := fetched[0].Source; got.Type != library.SourceGitHub || got.Label != "owner/repo" || got.Ref != "main" || got.ViewURL != "https://github.com/owner/repo/tree/main" || fetched[0].Revision != preview.Revision {
				t.Fatalf("source = %#v", fetched[0].Source)
			}
			if _, err := lib.Fetch(t.Context(), preview.ID, []string{"second"}); !library.IsCode(err, library.NotFound) {
				t.Fatalf("handle reused after successful fetch: %v", err)
			}
		})
	}
}

func TestGitHubFetchKeepsPreviewedContentAfterHeadAdvances(t *testing.T) {
	for _, mode := range gitHubModes {
		t.Run(mode.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"demo/SKILL.md": {content: []byte(skillDocument("demo", "pinned")), mode: 0o644},
				"demo/note.md":  {content: []byte("original note"), mode: 0o644},
			})
			advance := mode.serve(t, repo)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}

			writeFile(t, filepath.Join(repo, "demo", "note.md"), []byte("advanced note"), 0o644)
			commitGit(t, repo, "advance")
			advance()

			fetched := fetchSkills(t, lib, preview, []string{"demo"})
			want := []library.File{
				{Path: "SKILL.md", Content: []byte(skillDocument("demo", "pinned")), Mode: library.ModeRegular},
				{Path: "note.md", Content: []byte("original note"), Mode: library.ModeRegular},
			}
			if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), want) || fetched[0].Revision != preview.Revision {
				t.Fatalf("fetched = %#v, want previewed content at %s", fetched[0], preview.Revision)
			}
		})
	}
}

func TestGitHubPreviewSelectsBySubpathNameAndRoot(t *testing.T) {
	for _, mode := range gitHubModes {
		t.Run(mode.name+"/subpath and name", func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"skills/first/SKILL.md":  {content: []byte(skillDocument("first", "one")), mode: 0o644},
				"skills/second/SKILL.md": {content: []byte(skillDocument("second", "two")), mode: 0o644},
				"other/SKILL.md":         {content: []byte(skillDocument("other", "outside")), mode: 0o644},
			})
			mode.serve(t, repo)
			lib := openTestLibrary(t)
			request := gitHubRequest()
			request.Input = "owner/repo@first"
			request.Subpath = "skills"
			preview, err := lib.PreviewFetch(t.Context(), request)
			if err != nil || !reflect.DeepEqual(preview.Candidates, []library.Candidate{{Name: "first", Path: "skills/first"}}) {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
			fetched := fetchSkills(t, lib, preview, candidateNames(preview))
			if fetched[0].Source.Label != "owner/repo" {
				t.Fatalf("subpath changed source identity: %#v", fetched[0].Source)
			}
		})
		t.Run(mode.name+"/root skill", func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"SKILL.md":                   {content: []byte(skillDocument("root-skill", "root")), mode: 0o644},
				"notes.md":                   {content: []byte("extra"), mode: 0o644},
				"references/nested/SKILL.md": {content: []byte(skillDocument("nested", "keep")), mode: 0o644},
			})
			mode.serve(t, repo)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil || !reflect.DeepEqual(preview.Candidates, []library.Candidate{{Name: "root-skill", Path: "."}}) {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
			fetched := fetchSkills(t, lib, preview, candidateNames(preview))
			want := []library.File{
				{Path: "SKILL.md", Content: []byte(skillDocument("root-skill", "root")), Mode: library.ModeRegular},
				{Path: "notes.md", Content: []byte("extra"), Mode: library.ModeRegular},
				{Path: "references/nested/SKILL.md", Content: []byte(skillDocument("nested", "keep")), Mode: library.ModeRegular},
			}
			if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), want) {
				t.Fatalf("files = %#v", skillFiles(t, lib, fetched[0]))
			}
		})
	}
}

func TestGitHubCheckUpdatesAndUpdate(t *testing.T) {
	for _, mode := range gitHubModes {
		t.Run(mode.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"first/SKILL.md":     {content: []byte(skillDocument("first", "one")), mode: 0o644},
				"first/notes.md":     {content: []byte("v1"), mode: 0o644},
				"other-dir/SKILL.md": {content: []byte(skillDocument("second", "two")), mode: 0o644},
			})
			advance := mode.serve(t, repo)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			skills := fetchSkills(t, lib, preview, []string{"first", "second"})
			requireAvailable := func(want ...bool) {
				t.Helper()
				for i, skill := range skills {
					status, err := checkSkillUpdate(t, lib, t.Context(), skill.ID)
					if err != nil || (status == library.UpdateAvailable) != want[i] || (status != library.UpdateAvailable && status != library.UpdateCurrent) {
						t.Fatalf("check %s = %v, %v; want available %v", skill.Name, status, err, want[i])
					}
				}
			}
			requireAvailable(false, false)

			writeFile(t, filepath.Join(repo, "README.md"), []byte("outside every skill"), 0o644)
			commitGit(t, repo, "unrelated change")
			advance()
			requireAvailable(false, false)

			writeFile(t, filepath.Join(repo, "first", "notes.md"), []byte("v2"), 0o644)
			commitGit(t, repo, "change first")
			advance()
			requireAvailable(true, false)

			updated, err := updateSkill(t, lib, t.Context(), skills[0].ID)
			if err != nil || !updated.Changed {
				t.Fatalf("Update(first) = %#v, %v", updated, err)
			}
			want := library.File{Path: "notes.md", Content: []byte("v2"), Mode: library.ModeRegular}
			if !slices.ContainsFunc(skillFiles(t, lib, updated.Skill), func(file library.File) bool { return reflect.DeepEqual(file, want) }) || updated.Skill.Revision != gitRevParse(t, repo, "HEAD") {
				t.Fatalf("updated skill = %#v", updated.Skill)
			}
			// 同一来源里没变化的技能,随这次更新一起确认到新的来源版本,不产生新版本。
			if second, err := lib.GetSkill(t.Context(), skills[1].ID); err != nil || second.Revision != gitRevParse(t, repo, "HEAD") || second.VersionID != skills[1].VersionID {
				t.Fatalf("second after updating first = %#v, %v; want confirmed at HEAD", second, err)
			}
			unchanged, err := updateSkill(t, lib, t.Context(), skills[1].ID)
			if err != nil || unchanged.Changed || unchanged.Skill.Revision != gitRevParse(t, repo, "HEAD") || !unchanged.Skill.UpdatedAt.IsZero() {
				t.Fatalf("Update(second) = %#v, %v; want confirmed at HEAD without a new version", unchanged, err)
			}
			requireAvailable(false, false)
			if versions, err := lib.ListVersions(t.Context(), skills[0].ID); err != nil || len(versions) != 2 {
				t.Fatalf("versions = %#v, %v", versions, err)
			}
		})
	}
}

// GitHub 来源入库时要的文件多就先取整包 tar.gz,其余从 raw.githubusercontent.com 逐个取;两处的内容都按 git blob SHA 核对,
// 核对不上或取不到时才用 API 的 blob 接口。
func TestGitHubDownloadsVerifiedContentBeforeUsingBlobAPI(t *testing.T) {
	many := map[string]repoFile{}
	var skillFilesOfMany []string
	for i := range 10 {
		name := fmt.Sprintf("skill-%d", i)
		skillFilesOfMany = append(skillFilesOfMany, name+"/SKILL.md")
		many[name+"/SKILL.md"] = repoFile{content: []byte(skillDocument(name, "body "+name)), mode: 0o644}
		many[name+"/scripts/run.sh"] = repoFile{content: []byte("echo " + name), mode: 0o755}
	}
	few := map[string]repoFile{
		"alpha/SKILL.md":       {content: []byte(skillDocument("alpha", "body alpha")), mode: 0o644},
		"alpha/scripts/run.sh": {content: []byte("echo alpha"), mode: 0o755},
	}
	for _, tc := range []struct {
		name     string
		files    map[string]repoFile
		tamper   bool
		archives int
		raw      []string
	}{
		// blob 接口不可用,取到的内容只能来自整包或 raw:预览读的 SKILL.md 走 raw,入库要的 10 个脚本走一次整包。
		{name: "many files from one archive", files: many, archives: 1, raw: skillFilesOfMany},
		{name: "few files from raw", files: few, raw: []string{"alpha/SKILL.md", "alpha/scripts/run.sh"}},
		{name: "tampered downloads fall back to blob api", files: many, tamper: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, tc.files)
			api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
			blockGitHubClone(t)
			api.serveDownloads(true, true, tc.tamper)
			api.setFailBlobs(!tc.tamper)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.Candidates) != len(tc.files)/2 {
				t.Fatalf("candidates = %v", candidateNames(preview))
			}
			for _, skill := range fetchSkills(t, lib, preview, candidateNames(preview)) {
				want := []library.File{
					{Path: "SKILL.md", Content: tc.files[skill.Name+"/SKILL.md"].content, Mode: library.ModeRegular},
					{Path: "scripts/run.sh", Content: tc.files[skill.Name+"/scripts/run.sh"].content, Mode: library.ModeExecutable},
				}
				if got := skillFiles(t, lib, skill); !reflect.DeepEqual(got, want) {
					t.Fatalf("%s files = %#v", skill.Name, got)
				}
			}
			if tc.tamper {
				return
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if raw := slices.Sorted(slices.Values(api.rawPaths)); api.archiveRequests != tc.archives || !slices.Equal(raw, tc.raw) {
				t.Fatalf("archive requests %d, raw %v; want %d, %v", api.archiveRequests, raw, tc.archives, tc.raw)
			}
		})
	}
}

func TestGitHubFallsBackToCloneWhenAPIIsIncomplete(t *testing.T) {
	files := map[string]repoFile{
		"good/SKILL.md":    {content: []byte(skillDocument("good", "body")), mode: 0o644},
		"good/scripts/run": {content: []byte{9, 8, 7}, mode: 0o755},
	}
	for _, tc := range []struct {
		name  string
		serve func(t *testing.T, repo string)
	}{
		{"api unavailable", func(t *testing.T, repo string) { serveGitHubCloneOnly(t, repo) }},
		{"truncated tree", func(t *testing.T, repo string) {
			fx := inspectGitHubRepo(t, repo)
			fx.truncated = true
			serveGitHubAPI(t, fx)
			redirectGitHubClone(t, repo)
		}},
		{"incomplete blob", func(t *testing.T, repo string) {
			fx := inspectGitHubRepo(t, repo)
			fx.incompleteBlob = fx.blobSHA("good/SKILL.md")
			serveGitHubAPI(t, fx)
			redirectGitHubClone(t, repo)
		}},
		{"rate limited blobs", func(t *testing.T, repo string) {
			api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
			api.setBlobStatus(http.StatusTooManyRequests)
			api.rateLimitReset = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
			redirectGitHubClone(t, repo)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, files)
			tc.serve(t, repo)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil || len(preview.Candidates) != 1 {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
			fetched := fetchSkills(t, lib, preview, candidateNames(preview))
			want := []library.File{
				{Path: "SKILL.md", Content: []byte(skillDocument("good", "body")), Mode: library.ModeRegular},
				{Path: "scripts/run", Content: []byte{9, 8, 7}, Mode: library.ModeExecutable},
			}
			if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), want) {
				t.Fatalf("files = %#v", skillFiles(t, lib, fetched[0]))
			}
		})
	}
}

func TestGitHubSkillWithUnsupportedObjectIsReportedAsDiagnostic(t *testing.T) {
	for _, mode := range gitHubModes {
		t.Run(mode.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"good/SKILL.md": {content: []byte(skillDocument("good", "body")), mode: 0o644},
				"bad/SKILL.md":  {content: []byte(skillDocument("bad", "body")), mode: 0o644},
			})
			if err := os.Symlink("/does-not-exist", filepath.Join(repo, "bad", "link")); err != nil {
				t.Fatal(err)
			}
			commitGit(t, repo, "symlink")
			mode.serve(t, repo)
			redirectGitHubClone(t, repo)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "good" || len(preview.Diagnostics) != 1 || preview.Diagnostics[0].Path != "bad" {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
			fetchSkills(t, lib, preview, []string{"good"})
		})
	}
}

func TestGitHubFetchFailureLeavesLibraryUnchangedAndKeepsHandle(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(api *gitHubAPIMock)
		code library.ErrorCode
	}{
		{"unreadable blobs", func(api *gitHubAPIMock) { api.setFailBlobs(true) }, library.SourceUnreachable},
		{"forbidden blobs", func(api *gitHubAPIMock) { api.setBlobStatus(http.StatusForbidden) }, library.Permission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
				"demo/extra.md": {content: []byte("asset"), mode: 0o644},
			})
			api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
			blockGitHubClone(t)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			tc.fail(api)
			if fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"demo"}); !library.IsCode(err, tc.code) || len(fetched) != 0 {
				t.Fatalf("Fetch = %#v, %v; want %s", fetched, err, tc.code)
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("failed fetch wrote Library: %v, %v", skills, err)
			}
			redirectGitHubClone(t, repo)
			if fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"demo"}); err != nil || len(fetched) != 1 {
				t.Fatalf("retry = %#v, %v", fetched, err)
			}
		})
	}
}

func TestGitHubPrivateRepositoryUsesAvailableCredentials(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md":    {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/scripts/run": {content: []byte{0, 255, 1}, mode: 0o755},
	})
	fx := inspectGitHubRepo(t, repo)
	for _, credential := range []string{"environment token", "gh CLI"} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests} {
			t.Run(credential+"/"+http.StatusText(status), func(t *testing.T) {
				api := serveGitHubAPI(t, fx)
				api.anonymousStatus = status
				blockGitHubClone(t)
				if credential == "gh CLI" {
					serveGitHubCLI(t, fx)
				} else {
					t.Setenv("GITHUB_TOKEN", "test-github-token")
				}
				lib := openTestLibrary(t)
				preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
				if err != nil || len(preview.Candidates) != 1 {
					t.Fatalf("preview = %#v, %v", preview, err)
				}
				fetched := fetchSkills(t, lib, preview, candidateNames(preview))
				if len(skillFiles(t, lib, fetched[0])) != 2 {
					t.Fatalf("files = %#v", skillFiles(t, lib, fetched[0]))
				}
			})
		}
	}
}

// 调用方传入的令牌只放进「用令牌访问」这一步:仍先匿名访问,被拒后才带令牌;传入了就不再读环境变量里的令牌。
func TestGitHubPassedTokenReplacesEnvironmentTokenAfterAnonymous(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	api.acceptedToken = "passed-token"
	blockGitHubClone(t)
	t.Setenv("GITHUB_TOKEN", "environment-token")
	lib := openTestLibrary(t)
	setGitHubToken(t, lib, "passed-token")
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	fetched := fetchSkills(t, lib, preview, candidateNames(preview))
	if len(skillFiles(t, lib, fetched[0])) != 1 {
		t.Fatalf("files = %#v", skillFiles(t, lib, fetched[0]))
	}
	authorizations := api.takeAuthorizations()
	if len(authorizations) < 2 || authorizations[0] != "" {
		t.Fatalf("authorizations = %q, want anonymous first", authorizations)
	}
	for _, authorization := range authorizations[1:] {
		if authorization != "Bearer passed-token" {
			t.Fatalf("authorizations = %q, want passed token after anonymous", authorizations)
		}
	}
}

// 设置的令牌存在数据库里:重新打开同一个数据库,读到的仍是它,访问 GitHub 时也用它。
func TestGitHubTokenIsKeptInDatabase(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	api.acceptedToken = "stored-token"
	blockGitHubClone(t)
	path := filepath.Join(t.TempDir(), "library.db")
	first := openTestLibraryAt(t, path)
	setGitHubToken(t, first, "stored-token")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	lib := openTestLibraryAt(t, path)
	if token, err := lib.GitHubToken(t.Context()); err != nil || token != "stored-token" {
		t.Fatalf("GitHubToken after reopen = %q, %v", token, err)
	}
	if preview, err := lib.PreviewFetch(t.Context(), gitHubRequest()); err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	if got := api.takeAuthorizations(); len(got) < 2 || got[0] != "" || slices.ContainsFunc(got[1:], func(a string) bool { return a != "Bearer stored-token" }) {
		t.Fatalf("authorizations = %q, want anonymous then stored token", got)
	}
}

// 在同一个 Library 上改令牌,下一次访问 GitHub 就用新值:先前没有令牌、靠 gh 读到的私有仓库,传入令牌后
// 重新按匿名 → 令牌 → gh 的顺序访问;换成另一个令牌后用新令牌;清空后回到环境变量里的令牌。
func TestGitHubTokenChangeTakesEffectOnNextRequest(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	blockGitHubClone(t)
	serveGitHubCLI(t, fx)
	t.Setenv("GITHUB_TOKEN", "")
	lib := openTestLibrary(t)
	preview := func() {
		t.Helper()
		got, err := lib.PreviewFetch(t.Context(), gitHubRequest())
		if err != nil || len(got.Candidates) != 1 {
			t.Fatalf("preview = %#v, %v", got, err)
		}
	}
	preview()
	if got := api.takeAuthorizations(); !slices.Equal(got, []string{""}) {
		t.Fatalf("without token authorizations = %q, want anonymous then gh", got)
	}

	for _, step := range []struct {
		name, passed, environment, want string
	}{
		{"passed", "first-token", "", "Bearer first-token"},
		{"changed", "second-token", "", "Bearer second-token"},
		{"cleared", "", "environment-token", "Bearer environment-token"},
	} {
		t.Run(step.name, func(t *testing.T) {
			api.mu.Lock()
			api.acceptedToken = strings.TrimPrefix(step.want, "Bearer ")
			api.mu.Unlock()
			t.Setenv("GITHUB_TOKEN", step.environment)
			setGitHubToken(t, lib, step.passed)
			preview()
			got := api.takeAuthorizations()
			if len(got) < 2 || got[0] != "" || slices.ContainsFunc(got[1:], func(a string) bool { return a != step.want }) {
				t.Fatalf("authorizations = %q, want anonymous then %q", got, step.want)
			}
		})
	}
}

// 先靠 gh 做了入库预览,再传入令牌:对这个预览入库时也按匿名 → 令牌 → gh 重新访问,用上新令牌,不再直接走 gh。
func TestGitHubTokenChangeAppliesToExistingPreview(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/notes.md": {content: []byte("notes"), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	blockGitHubClone(t)
	serveGitHubCLI(t, fx)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	api.takeAuthorizations()

	api.mu.Lock()
	api.acceptedToken = "new-token"
	api.mu.Unlock()
	setGitHubToken(t, lib, "new-token")
	stubGitHubCLI(t, "exit 1\n")
	fetched := fetchSkills(t, lib, preview, candidateNames(preview))
	if len(skillFiles(t, lib, fetched[0])) != 2 {
		t.Fatalf("files = %#v", skillFiles(t, lib, fetched[0]))
	}
	got := api.takeAuthorizations()
	if len(got) < 2 || got[0] != "" || slices.ContainsFunc(got[1:], func(a string) bool { return a != "Bearer new-token" }) {
		t.Fatalf("authorizations = %q, want anonymous then new token", got)
	}
}

// 换令牌前发出、换令牌后才结束的请求,不会把它自己的访问结论(旧令牌被拒、改用 gh)带到新令牌上:
// 之后的新入库预览仍按匿名 → 新令牌访问。
func TestGitHubRequestFinishingAfterTokenChangeDoesNotBypassNewToken(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	api.acceptedToken = "old-token"
	blockGitHubClone(t)
	serveGitHubCLI(t, fx)
	gate := newNetworkGate()
	api.gateBlob(gate, fx.blobSHA("demo/SKILL.md"))
	lib := openTestLibrary(t)
	setGitHubToken(t, lib, "old-token")

	gate.arm()
	var wg sync.WaitGroup
	var oldErr error
	wg.Go(func() { _, oldErr = lib.PreviewFetch(t.Context(), gitHubRequest()) })
	gate.awaitEntered(t)
	api.mu.Lock()
	api.acceptedToken = "new-token"
	api.mu.Unlock()
	setGitHubToken(t, lib, "new-token")
	gate.open()
	wg.Wait()
	if oldErr != nil {
		t.Fatalf("old preview = %v, want success through gh", oldErr)
	}
	if got := api.takeAuthorizations(); !slices.Contains(got, "Bearer old-token") || slices.Contains(got, "Bearer new-token") {
		t.Fatalf("old preview authorizations = %q, want old token only", got)
	}

	stubGitHubCLI(t, "exit 1\n")
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("preview after token change = %#v, %v", preview, err)
	}
	if got := api.takeAuthorizations(); len(got) < 2 || got[0] != "" || slices.ContainsFunc(got[1:], func(a string) bool { return a != "Bearer new-token" }) {
		t.Fatalf("authorizations = %q, want anonymous then new token", got)
	}
}

func TestGitHubErrorsDoNotExposeCredentials(t *testing.T) {
	const secret = "private-token-and-cli-output"
	serveGitHubAPI(t, inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	redirectGitHubRemote(t, server.URL)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, candidateNames(preview))

	stubGitHubCLI(t, "echo '"+secret+"'\necho '"+secret+"' >&2\nexit 1\n")
	t.Setenv("GITHUB_TOKEN", secret)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, map[string]string{"message": secret}), nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"PreviewFetch", func() error { _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); return err }},
		{"CheckUpdates", func() error { _, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); return err }},
		{"Update", func() error { _, err := updateSkill(t, lib, t.Context(), fetched[0].ID); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			if !library.IsCode(err, library.Permission) || strings.Contains(fmt.Sprintf("%+v", err), secret) {
				t.Fatalf("error = %v, want Permission without credentials", err)
			}
		})
	}
}

func TestGitHubCancellationStopsChildProcesses(t *testing.T) {
	api := serveGitHubAPI(t, gitHubFixture{})
	api.anonymousStatus = http.StatusTooManyRequests
	blockGitHubClone(t)
	ready := filepath.Join(t.TempDir(), "ready")
	escaped := filepath.Join(t.TempDir(), "escaped")
	t.Setenv("GH_TEST_READY", ready)
	t.Setenv("GH_TEST_ESCAPED", escaped)
	stubGitHubCLI(t, `sh -c 'echo ready > "$GH_TEST_READY"; sleep 1; echo escaped > "$GH_TEST_ESCAPED"' &
wait
`)
	lib := openTestLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := lib.PreviewFetch(ctx, gitHubRequest())
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote operation did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled PreviewFetch = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled PreviewFetch did not stop promptly")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(escaped); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child process survived cancellation: %v", err)
	}
}

type repoFile struct {
	content []byte
	mode    os.FileMode
}

// gitHubFixture 是某个 commit 上仓库的 GitHub API 视图:commit、根 tree、全部条目与 blob 内容。
type gitHubFixture struct {
	commit         string
	message        string
	rootTree       string
	entries        []gitHubTreeEntry
	blobs          map[string][]byte
	paths          map[string]gitHubTreeEntry
	truncated      bool
	incompleteBlob string
}

type gitHubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size,omitempty"`
}

func (fx gitHubFixture) blobSHA(filePath string) string {
	return fx.paths[filePath].SHA
}

// gitHubRequest 是对 owner/repo 的 main 分支做入库预览的请求;显式给出 ref,不发现默认分支。
func gitHubRequest() library.PreviewFetchRequest {
	return library.PreviewFetchRequest{Input: "owner/repo", Ref: "main"}
}

func writeGitHubRepo(t *testing.T, files map[string]repoFile) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	for name, file := range files {
		writeFile(t, filepath.Join(repo, filepath.FromSlash(name)), file.content, file.mode)
	}
	commitGit(t, repo, "initial")
	return repo
}

func gitRevParse(t *testing.T, repository, spec string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", repository, "rev-parse", spec).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

// gitLog 返回 git log 的输出,去掉首尾空白。
func gitLog(t *testing.T, repository string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repository, "log"}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func inspectGitHubRepo(t *testing.T, repo string) gitHubFixture {
	t.Helper()
	fx := gitHubFixture{
		commit:   gitRevParse(t, repo, "HEAD"),
		message:  gitLog(t, repo, "--format=%B", "-1"),
		rootTree: gitRevParse(t, repo, "HEAD^{tree}"),
		blobs:    map[string][]byte{},
		paths:    map[string]gitHubTreeEntry{},
	}
	output, err := exec.Command("git", "-c", "core.excludesFile=/dev/null", "-C", repo, "ls-tree", "-r", "-t", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		meta, filePath, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			t.Fatalf("ls-tree: %q", line)
		}
		entry := gitHubTreeEntry{Path: filePath, Mode: fields[0], Type: fields[1], SHA: fields[2]}
		if entry.Type == "blob" {
			content, err := exec.Command("git", "-c", "core.excludesFile=/dev/null", "-C", repo, "cat-file", "-p", entry.SHA).Output()
			if err != nil {
				t.Fatal(err)
			}
			entry.Size = len(content)
			fx.blobs[entry.SHA] = content
		}
		fx.entries = append(fx.entries, entry)
		fx.paths[filePath] = entry
	}
	return fx
}

func blockGitHubClone(t *testing.T) {
	t.Helper()
	redirectGitHubClone(t, filepath.Join(t.TempDir(), "missing-remote"))
}

func redirectGitHubClone(t *testing.T, repo string) {
	t.Helper()
	redirectGitHubRemote(t, "file://"+repo)
}

// redirectGitHubRemote 把 owner/repo 的 HTTPS 与 SSH 远端都改写到 remote,并隔离本机 gh 登录态。
func redirectGitHubRemote(t *testing.T, remote string) {
	t.Helper()
	stubGitHubCLI(t, "exit 1\n")
	config := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, t.TempDir(), "config", "--file", config, "url."+remote+".insteadOf", "https://github.com/owner/repo.git")
	runGit(t, t.TempDir(), "config", "--file", config, "--add", "url."+remote+".insteadOf", "git@github.com:owner/repo.git")
}

// serveGitHubCloneOnly 让 GitHub API 一律 500,并把 owner/repo 的克隆重定向到本地仓库 repo。
func serveGitHubCloneOnly(t *testing.T, repo string) {
	t.Helper()
	serveGitHubRemoteOnly(t, "file://"+repo)
}

// serveGitHubRemoteOnly 让 GitHub API 一律 500,并把 owner/repo 的 git 远端改写到 remote。
func serveGitHubRemoteOnly(t *testing.T, remote string) {
	t.Helper()
	redirectGitHubRemote(t, remote)
	stubHTTPResponse(t, http.StatusInternalServerError, strings.NewReader("boom"))
}

// gitHubCloneRequest 让 owner/repo 走浅克隆并指向本地仓库 repository,返回对其 main 分支做入库预览的请求。
func gitHubCloneRequest(t *testing.T, repository string) library.PreviewFetchRequest {
	t.Helper()
	serveGitHubCloneOnly(t, repository)
	return gitHubRequest()
}

// gitHubAPIMock 按 gitHubFixture 回答 GitHub REST API;Swap 后旧 commit 及其 tree 仍可按 SHA 取到。
type gitHubAPIMock struct {
	mu                 sync.Mutex
	fx                 gitHubFixture
	history            []gitHubFixture
	anonymousStatus    int
	failBlobs          bool
	blobStatus         int
	rateLimitRemaining string
	rateLimitReset     string
	// blobGate 若非 nil,对 gatedBlob 这个 SHA 的 blob 请求先过闸,用来把操作停在网络阶段。
	blobGate  *networkGate
	gatedBlob string
	// acceptedToken 若非空,带令牌的请求只接受这一个令牌,其余令牌返回 401。
	acceptedToken string
	// authorizations 按顺序记下每个 API 请求的 Authorization 头,匿名请求记为空串。
	authorizations []string
	// archives、rawFiles 为 true 时,替身也回答 codeload.github.com 的整包 tar.gz 与 raw.githubusercontent.com 的单个文件,
	// 内容取自对应 commit;tamperDownloads 时这两处给出的内容都被改过。默认两处都不回答,内容只能经 API 的 blob 接口取到。
	// archiveRequests、rawPaths 记下两处回答过的整包请求次数与文件路径。
	archives, rawFiles, tamperDownloads bool
	archiveRequests                     int
	rawPaths                            []string
}

// takeAuthorizations 返回并清空已记下的 Authorization 头。
func (m *gitHubAPIMock) takeAuthorizations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	taken := m.authorizations
	m.authorizations = nil
	return taken
}

func serveGitHubAPI(t *testing.T, fx gitHubFixture) *gitHubAPIMock {
	t.Helper()
	stubGitHubCLI(t, "exit 1\n")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	mock := &gitHubAPIMock{fx: cloneGitHubFixture(fx)}
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(mock.roundTrip)}
	t.Cleanup(func() { http.DefaultClient = client })
	return mock
}

func cloneGitHubFixture(fx gitHubFixture) gitHubFixture {
	cloned := fx
	cloned.blobs = maps.Clone(fx.blobs)
	cloned.paths = maps.Clone(fx.paths)
	cloned.entries = slices.Clone(fx.entries)
	return cloned
}

func (m *gitHubAPIMock) Swap(fx gitHubFixture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, m.fx)
	next := cloneGitHubFixture(fx)
	for sha, content := range m.fx.blobs {
		if _, ok := next.blobs[sha]; !ok {
			next.blobs[sha] = content
		}
	}
	m.fx = next
}

func (m *gitHubAPIMock) serveDownloads(archives, rawFiles, tamper bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.archives, m.rawFiles, m.tamperDownloads = archives, rawFiles, tamper
}

func (m *gitHubAPIMock) setFailBlobs(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failBlobs = fail
}

func (m *gitHubAPIMock) setBlobStatus(status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobStatus = status
}

// gateBlob 让指定 SHA 的 blob 请求受 gate 控制;须在发出请求前调用。
func (m *gitHubAPIMock) gateBlob(gate *networkGate, sha string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobGate, m.gatedBlob = gate, sha
}

func (m *gitHubAPIMock) head(match func(gitHubFixture) bool) (gitHubFixture, bool) {
	heads := append([]gitHubFixture{m.fx}, m.history...)
	i := slices.IndexFunc(heads, match)
	if i < 0 {
		return gitHubFixture{}, false
	}
	return heads[i], true
}

func (m *gitHubAPIMock) roundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.github.com" {
		return m.download(r)
	}
	m.mu.Lock()
	gate, gated := m.blobGate, m.gatedBlob
	m.mu.Unlock()
	if gate != nil && r.URL.Path == "/repos/owner/repo/git/blobs/"+gated {
		gate.pass()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	authorization := r.Header.Get("Authorization")
	m.authorizations = append(m.authorizations, authorization)
	if m.anonymousStatus != 0 && authorization == "" {
		return m.errorResponse(m.anonymousStatus), nil
	}
	if m.acceptedToken != "" && authorization != "" && authorization != "Bearer "+m.acceptedToken {
		return m.errorResponse(http.StatusUnauthorized), nil
	}
	notFound := jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"})
	if ref, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/commits/"); ok {
		head, found := m.fx, ref == "main"
		if !found {
			head, found = m.head(func(h gitHubFixture) bool { return strings.EqualFold(ref, h.commit) })
		}
		if !found {
			return notFound, nil
		}
		return jsonResponse(http.StatusOK, map[string]any{"sha": head.commit, "commit": map[string]any{"tree": map[string]string{"sha": head.rootTree}}}), nil
	}
	if span, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/compare/"); ok {
		base, head, _ := strings.Cut(span, "...")
		heads := append(slices.Clone(m.history), m.fx)
		from := slices.IndexFunc(heads, func(h gitHubFixture) bool { return h.commit == base })
		to := slices.IndexFunc(heads, func(h gitHubFixture) bool { return h.commit == head })
		if from < 0 || to < from {
			return notFound, nil
		}
		var commits []any
		for _, h := range heads[from+1 : to+1] {
			commits = append(commits, map[string]any{"sha": h.commit, "commit": map[string]any{"message": h.message, "author": map[string]any{"name": "Test", "date": "2026-10-01T00:00:00Z"}}})
		}
		total := len(commits)
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if perPage > 0 && page > 0 {
			commits = commits[min((page-1)*perPage, total):min(page*perPage, total)]
		}
		return jsonResponse(http.StatusOK, map[string]any{"status": "ahead", "total_commits": total, "commits": commits}), nil
	}
	if sha, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/git/trees/"); ok {
		head, found := m.head(func(h gitHubFixture) bool { return sha == h.rootTree || sha == h.commit })
		if !found {
			return notFound, nil
		}
		return jsonResponse(http.StatusOK, map[string]any{"sha": head.rootTree, "tree": head.entries, "truncated": head.truncated}), nil
	}
	if sha, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/git/blobs/"); ok {
		if m.blobStatus != 0 {
			return m.errorResponse(m.blobStatus), nil
		}
		if m.failBlobs || sha == m.fx.incompleteBlob {
			return jsonResponse(http.StatusOK, map[string]any{"sha": sha, "encoding": "base64", "content": "", "size": 99}), nil
		}
		content, ok := m.fx.blobs[sha]
		if !ok {
			return notFound, nil
		}
		return jsonResponse(http.StatusOK, map[string]any{"sha": sha, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content), "size": len(content)}), nil
	}
	return notFound, nil
}

func (m *gitHubAPIMock) download(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	unavailable := &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody, Header: make(http.Header)}
	at := func(commit string) (gitHubFixture, bool) {
		return m.head(func(h gitHubFixture) bool { return h.commit == commit })
	}
	content := func(blob []byte) []byte {
		if m.tamperDownloads {
			return append(slices.Clone(blob), " tampered"...)
		}
		return blob
	}
	switch r.URL.Host {
	case "raw.githubusercontent.com":
		commit, filePath, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/owner/repo/"), "/")
		fx, ok := at(commit)
		if !m.rawFiles || !ok {
			return unavailable, nil
		}
		entry, ok := fx.paths[filePath]
		if !ok || entry.Type != "blob" {
			return textResponse(http.StatusNotFound, "404: Not Found"), nil
		}
		m.rawPaths = append(m.rawPaths, filePath)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(content(fx.blobs[entry.SHA]))), Header: make(http.Header)}, nil
	case "codeload.github.com":
		commit, ok := strings.CutPrefix(r.URL.Path, "/owner/repo/tar.gz/")
		fx, found := at(commit)
		if !m.archives || !ok || !found {
			return unavailable, nil
		}
		m.archiveRequests++
		var buffer bytes.Buffer
		zipped := gzip.NewWriter(&buffer)
		writer := tar.NewWriter(zipped)
		// 和 git archive 打的包一样:先是记着 commit 的 pax 全局头,再是顶层目录,每个目录各有一个条目。
		prefix := "repo-" + commit + "/"
		if err := writer.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": commit}}); err != nil {
			return nil, err
		}
		if err := writer.WriteHeader(&tar.Header{Name: prefix, Mode: 0o775, Typeflag: tar.TypeDir}); err != nil {
			return nil, err
		}
		for _, entry := range fx.entries {
			if entry.Type == "tree" {
				if err := writer.WriteHeader(&tar.Header{Name: prefix + entry.Path + "/", Mode: 0o775, Typeflag: tar.TypeDir}); err != nil {
					return nil, err
				}
				continue
			}
			mode, err := strconv.ParseInt(entry.Mode, 8, 64)
			if err != nil {
				return nil, err
			}
			blob := content(fx.blobs[entry.SHA])
			if err := writer.WriteHeader(&tar.Header{Name: prefix + entry.Path, Mode: mode & 0o777, Size: int64(len(blob)), Typeflag: tar.TypeReg}); err != nil {
				return nil, err
			}
			if _, err := writer.Write(blob); err != nil {
				return nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		if err := zipped.Close(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(&buffer), Header: make(http.Header)}, nil
	}
	return unavailable, nil
}

func (m *gitHubAPIMock) errorResponse(status int) *http.Response {
	response := jsonResponse(status, map[string]string{"message": "unavailable"})
	response.Header.Set("X-RateLimit-Remaining", m.rateLimitRemaining)
	response.Header.Set("X-RateLimit-Reset", m.rateLimitReset)
	return response
}

func jsonResponse(status int, body any) *http.Response {
	payload, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(payload))), Header: header}
}

// serveGitHubCLI 用一个只回答 fx 的 gh 脚本模拟已登录的 GitHub CLI。
func serveGitHubCLI(t *testing.T, fx gitHubFixture) {
	t.Helper()
	var script strings.Builder
	script.WriteString(`[ "$1" = api ] || exit 1
case "$2" in
`)
	respond := func(endpoint string, payload any) {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&script, "'%s') cat <<'JSON'\n%s\nJSON\n;;\n", endpoint, body)
	}
	respond("/repos/owner/repo/commits/main", map[string]any{"sha": fx.commit, "commit": map[string]any{"tree": map[string]string{"sha": fx.rootTree}}})
	respond("/repos/owner/repo/git/trees/"+fx.rootTree+"?recursive=1", map[string]any{"tree": fx.entries, "truncated": fx.truncated})
	for sha, content := range fx.blobs {
		respond("/repos/owner/repo/git/blobs/"+sha, map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(content), "size": len(content)})
	}
	script.WriteString("*) exit 1 ;;\nesac\n")
	stubGitHubCLI(t, script.String())
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
			if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateAvailable {
				t.Fatalf("check = %v, %v", status, err)
			}
			updated, err := updateSkill(t, lib, t.Context(), id)
			if err != nil || !updated.Changed || string(skillFiles(t, lib, updated.Skill)[0].Content) != skillDocument("same", "changed") {
				t.Fatalf("update = %#v, %v", updated, err)
			}
			runGit(t, repo, "mv", "skills", "examples")
			writeFile(t, filepath.Join(repo, "skills/other/SKILL.md"), []byte(skillDocument("other", "keep conventional")), 0o644)
			commitGit(t, repo, "move out of discovery")
			if api != nil {
				api.Swap(inspectGitHubRepo(t, repo))
			}
			if _, err := updateSkill(t, lib, t.Context(), id); !library.IsCode(err, library.NotFound) {
				t.Fatalf("missing update = %v", err)
			}
			runGit(t, repo, "rm", "-r", "skills", "examples")
			writeFile(t, filepath.Join(repo, "README.md"), []byte("no skills"), 0o644)
			commitGit(t, repo, "remove candidates")
			if api != nil {
				api.Swap(inspectGitHubRepo(t, repo))
			}
			if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateDeleted {
				t.Fatalf("empty source = %v, %v", status, err)
			}
		})
	}
}

func TestUpdatePlanReadsEveryPageOfCommits(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"a/SKILL.md": {content: []byte(skillDocument("a", "v1")), mode: 0o644}})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	a := fetchSkills(t, lib, preview, []string{"a"})[0]
	const count = 230
	for i := range count {
		next := fx
		next.commit = fmt.Sprintf("%040x", i+1)
		next.message = fmt.Sprintf("commit %d", i+1)
		api.Swap(next)
	}
	plan, err := lib.PlanUpdate(t.Context(), a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Commits) != count || plan.Commits[0].Message != fmt.Sprintf("commit %d", count) || plan.Commits[count-1].Message != "commit 1" {
		t.Fatalf("commits = %d, first %#v; want all %d newest first", len(plan.Commits), plan.Commits[0], count)
	}
}
