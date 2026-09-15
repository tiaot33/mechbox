package library_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestGitHubCheckAndUpdateUseDirectoryTreeSHAWithoutFetchingFiles(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":      {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/scripts/run":   {content: []byte{0, 255, 1}, mode: 0o755},
		"second/SKILL.md":     {content: []byte(skillDocument("second", "second body")), mode: 0o644},
		"second/notes.md":     {content: []byte("second notes"), mode: 0o644},
		"unrelated/large.bin": {content: []byte("noise"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 2 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	first, second := installed[0], installed[1]
	if first.Name != "first" {
		first, second = second, first
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := lib.GetSkill(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, first.ID); err != nil || available {
		t.Fatalf("unchanged CheckUpdate = %v, %v", available, err)
	}
	// 安装时钉住的 commit 未变:只解析 commit,不取 tree。
	assertGitHubCommitOnly(t, api, 1)
	current, err := lib.GetSkill(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(current, before) {
		t.Fatalf("check wrote Library: %#v, %v", current, err)
	}

	writeFile(t, filepath.Join(repo, "unrelated", "large.bin"), []byte("more noise"), 0o644)
	commitGit(t, repo, "unrelated path")
	other := inspectGitHubRepo(t, repo)
	if other.treeSHA("first") != fx.treeSHA("first") || other.commit == fx.commit {
		t.Fatal("fixture did not keep skill SHA while advancing commit")
	}
	api.Swap(other)
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, first.ID); err != nil || available {
		t.Fatalf("unrelated path CheckUpdate = %v, %v", available, err)
	}
	if available, err := lib.CheckUpdate(ctx, second.ID); err != nil || available {
		t.Fatalf("unrelated path second CheckUpdate = %v, %v", available, err)
	}
	// commit 前进、目录未变:要取 tree 比较目录 SHA,但不取 Skill 文件。
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	stale, err := lib.GetSkill(ctx, first.ID)
	if err != nil || stale.Source.LastSeenRef != fx.commit {
		t.Fatalf("check refreshed last_seen_ref: %#v, %v", stale, err)
	}

	result, err := lib.Update(ctx, first.ID)
	if err != nil || result.Changed || result.Note == "" {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	if result.Skill.GitTreeSHA != fx.treeSHA("first") {
		t.Fatalf("unchanged Update GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, fx.treeSHA("first"))
	}
	if result.Skill.Source.LastSeenRef != other.commit {
		t.Fatalf("unchanged Update last_seen_ref = %q, want new commit %q", result.Skill.Source.LastSeenRef, other.commit)
	}
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update created version: %#v, %v", versions, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)

	writeFile(t, filepath.Join(repo, "first", "SKILL.md"), []byte(skillDocument("first", "updated body")), 0o644)
	writeFile(t, filepath.Join(repo, "first", "scripts", "run"), []byte{9, 8, 7}, 0o755)
	commitGit(t, repo, "change first")
	changed := inspectGitHubRepo(t, repo)
	if changed.treeSHA("first") == fx.treeSHA("first") {
		t.Fatal("first skill tree SHA did not change")
	}
	if changed.treeSHA("second") != fx.treeSHA("second") {
		t.Fatal("second skill tree SHA changed")
	}
	api.Swap(changed)
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, first.ID); err != nil || !available {
		t.Fatalf("changed CheckUpdate = %v, %v", available, err)
	}
	if available, err := lib.CheckUpdate(ctx, second.ID); err != nil || available {
		t.Fatalf("unchanged sibling CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	afterCheck, err := lib.GetSkill(ctx, first.ID)
	if err != nil || string(afterCheck.Files[0].Content) != skillDocument("first", "first body") {
		t.Fatalf("check mutated files: %#v, %v", afterCheck, err)
	}
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("check created version: %#v, %v", versions, err)
	}

	api.Reset()
	checks, err := lib.CheckUpdates(ctx, []library.SkillID{first.ID, second.ID, first.ID})
	if err != nil || len(checks) != 2 || checks[0].SkillID != first.ID || checks[1].SkillID != second.ID {
		t.Fatalf("CheckUpdates = %#v, %v", checks, err)
	}
	if checks[0].Error != nil || !checks[0].Available || checks[1].Error != nil || checks[1].Available {
		t.Fatalf("CheckUpdates availability = %#v", checks)
	}
	_, trees, _ := gitHubAPIKindCounts(t, api)
	if trees != 1 {
		t.Fatalf("CheckUpdates trees=%d (%v), want one tree", trees, api.snapshot())
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("CheckUpdates wrote history: %#v, %v", versions, err)
	}

	api.Reset()
	result, err = lib.Update(ctx, first.ID)
	if err != nil || !result.Changed || result.Note == "" {
		t.Fatalf("changed Update = %#v, %v", result, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("first", "updated body")), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{9, 8, 7}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(result.Skill.Files, wantFiles) {
		t.Fatalf("updated files = %#v, want %#v", result.Skill.Files, wantFiles)
	}
	if result.Skill.GitTreeSHA != changed.treeSHA("first") {
		t.Fatalf("updated GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, changed.treeSHA("first"))
	}
	if got := readPersistedGitTreeSHA(t, lib, first.ID); got != changed.treeSHA("first") {
		t.Fatalf("persisted GitTreeSHA = %q, want %q", got, changed.treeSHA("first"))
	}
	assertGitHubUpdateBlobs(t, api, changed, "first")
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 2 {
		t.Fatalf("versions after change = %#v, %v", versions, err)
	}
	content, err := os.ReadFile(filepath.Join(directory, first.Name, "SKILL.md"))
	if err != nil || string(content) != skillDocument("first", "first body") {
		t.Fatalf("projection rewritten: %q, %v", content, err)
	}
	after, err := lib.ListActivations(ctx)
	if err != nil || !reflect.DeepEqual(after, activations) {
		t.Fatalf("activation records changed: %#v, %v", after, err)
	}
}

func TestGitHubCheckUpdateMatchesSkillNameNotDirectoryName(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"foo/SKILL.md":   {content: []byte(skillDocument("bar", "bar body")), mode: 0o644},
		"foo/data.bin":   {content: []byte{1, 2, 3}, mode: 0o644},
		"other/SKILL.md": {content: []byte(skillDocument("foo", "foo body")), mode: 0o644},
		"other/notes.md": {content: []byte("foo notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 2 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	var foo, bar library.Skill
	for _, skill := range installed {
		switch skill.Name {
		case "foo":
			foo = skill
		case "bar":
			bar = skill
		}
	}
	if foo.GitTreeSHA != fx.treeSHA("other") || bar.GitTreeSHA != fx.treeSHA("foo") {
		t.Fatalf("installed SHAs foo=%q bar=%q, want other=%q foo=%q", foo.GitTreeSHA, bar.GitTreeSHA, fx.treeSHA("other"), fx.treeSHA("foo"))
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, foo.ID); err != nil || available {
		t.Fatalf("foo CheckUpdate = %v, %v", available, err)
	}
	if available, err := lib.CheckUpdate(ctx, bar.ID); err != nil || available {
		t.Fatalf("bar CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 2)

	writeFile(t, filepath.Join(repo, "foo", "SKILL.md"), []byte(skillDocument("bar", "changed bar")), 0o644)
	commitGit(t, repo, "change directory foo")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, foo.ID); err != nil || available {
		t.Fatalf("unchanged skill foo CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, bar.ID); err != nil || !available {
		t.Fatalf("changed skill bar CheckUpdate = %v, %v", available, err)
	}
	result, err := lib.Update(ctx, bar.ID)
	if err != nil || !result.Changed {
		t.Fatalf("Update bar = %#v, %v", result, err)
	}
	if string(result.Skill.Files[0].Content) != skillDocument("bar", "changed bar") {
		t.Fatalf("Update bar wrote the other skill: %#v", result.Skill.Files)
	}
	if result.Skill.GitTreeSHA != changed.treeSHA("foo") {
		t.Fatalf("bar GitTreeSHA = %q, want directory foo %q", result.Skill.GitTreeSHA, changed.treeSHA("foo"))
	}
}

func TestGitHubEmptyTreeSHAFallsBackToFilesThenBackfillsOnUpdate(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"legacy/SKILL.md":  {content: []byte(skillDocument("legacy", "old body")), mode: 0o644},
		"legacy/data.bin":  {content: []byte{1, 2, 3}, mode: 0o644},
		"sibling/SKILL.md": {content: []byte(skillDocument("sibling", "sibling body")), mode: 0o644},
		"sibling/notes.md": {content: []byte("notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openV1GitHubLibrary(t, fx.commit, []v1GitHubSkill{
		{name: "legacy", files: []library.File{
			{Path: "SKILL.md", Content: []byte(skillDocument("legacy", "old body")), Mode: library.ModeRegular},
			{Path: "data.bin", Content: []byte{1, 2, 3}, Mode: library.ModeRegular},
		}},
		{name: "sibling", files: []library.File{
			{Path: "SKILL.md", Content: []byte(skillDocument("sibling", "sibling body")), Mode: library.ModeRegular},
			{Path: "notes.md", Content: []byte("notes"), Mode: library.ModeRegular},
		}},
	})
	ctx := t.Context()
	legacy := v1GitHubSkillByName(t, lib, "legacy")
	sibling := v1GitHubSkillByName(t, lib, "sibling")
	if legacy.GitTreeSHA != "" || sibling.GitTreeSHA != "" {
		t.Fatalf("migrated GitTreeSHA legacy=%q sibling=%q, want empty", legacy.GitTreeSHA, sibling.GitTreeSHA)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, legacy.ID); err != nil || available {
		t.Fatalf("empty SHA same files CheckUpdate = %v, %v", available, err)
	}
	if _, _, blobs := gitHubAPIKindCounts(t, api); blobs == 0 {
		t.Fatal("empty SHA check did not read skill files")
	}
	if got := readPersistedGitTreeSHA(t, lib, legacy.ID); got != "" {
		t.Fatalf("check backfilled SHA: %q", got)
	}

	api.Reset()
	result, err := lib.Update(ctx, legacy.ID)
	if err != nil || result.Changed {
		t.Fatalf("empty SHA same files Update = %#v, %v", result, err)
	}
	if result.Skill.GitTreeSHA != fx.treeSHA("legacy") {
		t.Fatalf("backfilled GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, fx.treeSHA("legacy"))
	}
	if got := readPersistedGitTreeSHA(t, lib, legacy.ID); got != fx.treeSHA("legacy") {
		t.Fatalf("persisted backfill = %q, want %q", got, fx.treeSHA("legacy"))
	}
	if versions, err := lib.ListVersions(ctx, legacy.ID); err != nil || len(versions) != 1 {
		t.Fatalf("backfill created version: %#v, %v", versions, err)
	}

	// 补写 SHA 的同时补写了确认 commit:旧记录经一次 Update 就进入只解析 commit 的快路径。
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, legacy.ID); err != nil || available {
		t.Fatalf("after backfill CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)

	writeFile(t, filepath.Join(repo, "sibling", "notes.md"), []byte("changed notes"), 0o644)
	commitGit(t, repo, "change sibling")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, sibling.ID); err != nil || !available {
		t.Fatalf("empty SHA changed files CheckUpdate = %v, %v", available, err)
	}
	result, err = lib.Update(ctx, sibling.ID)
	if err != nil || !result.Changed {
		t.Fatalf("empty SHA changed files Update = %#v, %v", result, err)
	}
	if result.Skill.GitTreeSHA != changed.treeSHA("sibling") {
		t.Fatalf("new version GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, changed.treeSHA("sibling"))
	}
	if versions, err := lib.ListVersions(ctx, sibling.ID); err != nil || len(versions) != 2 {
		t.Fatalf("changed empty SHA versions = %#v, %v", versions, err)
	}
}

func TestGitHubUpdateDownloadsOnlyChangedBlob(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md":    {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/notes.md":    {content: []byte("old notes"), mode: 0o644},
		"demo/scripts/run": {content: []byte("echo old"), mode: 0o755},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}

	writeFile(t, filepath.Join(repo, "demo", "notes.md"), []byte("new notes"), 0o644)
	commitGit(t, repo, "change notes")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	api.Reset()
	result, err := lib.Update(ctx, installed[0].ID)
	if err != nil || !result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("demo", "body")), Mode: library.ModeRegular},
		{Path: "notes.md", Content: []byte("new notes"), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte("echo old"), Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(result.Skill.Files, wantFiles) {
		t.Fatalf("updated files = %#v, want %#v", result.Skill.Files, wantFiles)
	}
	assertGitHubBlobPaths(t, api, "demo/notes.md")

	versions, err := lib.ListVersions(ctx, installed[0].ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	oldVer, err := lib.GetVersion(ctx, versions[0].ID)
	if err != nil || !reflect.DeepEqual(oldVer.Files, installed[0].Files) {
		t.Fatalf("GetVersion old = %#v, %v", oldVer, err)
	}
	newVer, err := lib.GetVersion(ctx, versions[1].ID)
	if err != nil || !reflect.DeepEqual(newVer.Files, wantFiles) {
		t.Fatalf("GetVersion new = %#v, %v", newVer, err)
	}
	diff, err := lib.DiffVersions(ctx, versions[0].ID, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := library.VersionDiff{Changed: []string{"notes.md"}}
	if !reflect.DeepEqual(diff, wantDiff) {
		t.Fatalf("DiffVersions = %#v, want %#v", diff, wantDiff)
	}
}

func TestGitHubUpdateReusesBlobWhenOnlyExecutableBitFlips(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md":    {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/notes.md":    {content: []byte("notes"), mode: 0o644},
		"demo/scripts/run": {content: []byte("echo run"), mode: 0o755},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}

	if err := os.Chmod(filepath.Join(repo, "demo", "scripts", "run"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repo, "drop executable")
	changed := inspectGitHubRepo(t, repo)
	if changed.blobSHA("demo/scripts/run") != fx.blobSHA("demo/scripts/run") {
		t.Fatal("fixture changed blob SHA when only mode flipped")
	}
	api.Swap(changed)
	api.Reset()
	result, err := lib.Update(ctx, installed[0].ID)
	if err != nil || !result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("demo", "body")), Mode: library.ModeRegular},
		{Path: "notes.md", Content: []byte("notes"), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte("echo run"), Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(result.Skill.Files, wantFiles) {
		t.Fatalf("updated files = %#v, want %#v", result.Skill.Files, wantFiles)
	}
	assertGitHubBlobPaths(t, api)

	versions, err := lib.ListVersions(ctx, installed[0].ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	newVer, err := lib.GetVersion(ctx, versions[1].ID)
	if err != nil || !reflect.DeepEqual(newVer.Files, wantFiles) {
		t.Fatalf("GetVersion new = %#v, %v", newVer, err)
	}
	diff, err := lib.DiffVersions(ctx, versions[0].ID, versions[1].ID)
	if err != nil || len(diff.Changed)+len(diff.Added)+len(diff.Deleted) != 0 {
		t.Fatalf("DiffVersions = %#v, %v, want empty (mode-only)", diff, err)
	}
}

func TestGitHubUpdateDownloadsOnlyAddedBlob(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/notes.md": {content: []byte("notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}

	writeFile(t, filepath.Join(repo, "demo", "extra.md"), []byte("new file"), 0o644)
	commitGit(t, repo, "add extra")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	api.Reset()
	result, err := lib.Update(ctx, installed[0].ID)
	if err != nil || !result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("demo", "body")), Mode: library.ModeRegular},
		{Path: "extra.md", Content: []byte("new file"), Mode: library.ModeRegular},
		{Path: "notes.md", Content: []byte("notes"), Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(result.Skill.Files, wantFiles) {
		t.Fatalf("updated files = %#v, want %#v", result.Skill.Files, wantFiles)
	}
	assertGitHubBlobPaths(t, api, "demo/extra.md")

	versions, err := lib.ListVersions(ctx, installed[0].ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	newVer, err := lib.GetVersion(ctx, versions[1].ID)
	if err != nil || !reflect.DeepEqual(newVer.Files, wantFiles) {
		t.Fatalf("GetVersion new = %#v, %v", newVer, err)
	}
	diff, err := lib.DiffVersions(ctx, versions[0].ID, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := library.VersionDiff{Added: []string{"extra.md"}}
	if !reflect.DeepEqual(diff, wantDiff) {
		t.Fatalf("DiffVersions = %#v, want %#v", diff, wantDiff)
	}
}

func TestGitHubUpdateReusesBlobWhenFileMoves(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/notes.md": {content: []byte("same notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}

	if err := os.MkdirAll(filepath.Join(repo, "demo", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, "demo", "notes.md"), filepath.Join(repo, "demo", "docs", "notes.md")); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repo, "move notes")
	changed := inspectGitHubRepo(t, repo)
	if changed.blobSHA("demo/docs/notes.md") != fx.blobSHA("demo/notes.md") {
		t.Fatal("moved file blob SHA changed")
	}
	api.Swap(changed)
	api.Reset()
	result, err := lib.Update(ctx, installed[0].ID)
	if err != nil || !result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("demo", "body")), Mode: library.ModeRegular},
		{Path: "docs/notes.md", Content: []byte("same notes"), Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(result.Skill.Files, wantFiles) {
		t.Fatalf("updated files = %#v, want %#v", result.Skill.Files, wantFiles)
	}
	assertGitHubBlobPaths(t, api)

	versions, err := lib.ListVersions(ctx, installed[0].ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	newVer, err := lib.GetVersion(ctx, versions[1].ID)
	if err != nil || !reflect.DeepEqual(newVer.Files, wantFiles) {
		t.Fatalf("GetVersion new = %#v, %v", newVer, err)
	}
	diff, err := lib.DiffVersions(ctx, versions[0].ID, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := library.VersionDiff{Added: []string{"docs/notes.md"}, Deleted: []string{"notes.md"}}
	if !reflect.DeepEqual(diff, wantDiff) {
		t.Fatalf("DiffVersions = %#v, want %#v", diff, wantDiff)
	}
}

func TestGitHubUpdateFallsBackToCloneWhenBlobsFailAfterTree(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "old")), mode: 0o644},
		"demo/data.bin": {content: []byte{1, 2, 3}, mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	redirectGitHubClone(t, repo)

	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}

	writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", "new")), 0o644)
	writeFile(t, filepath.Join(repo, "demo", "data.bin"), []byte{9, 8, 7}, 0o644)
	commitGit(t, repo, "change demo")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	api.setFailBlobs(true)
	// API 已给出该 commit 的 tree,克隆时 HEAD 再前进也不能混用内容与 SHA。
	writeFile(t, filepath.Join(repo, "demo", "data.bin"), []byte("later HEAD"), 0o644)
	commitGit(t, repo, "advance after API snapshot")

	result, err := lib.Update(ctx, installed[0].ID)
	if err != nil || !result.Changed {
		t.Fatalf("blob-fail Update = %#v, %v", result, err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("demo", "new")), Mode: library.ModeRegular},
		{Path: "data.bin", Content: []byte{9, 8, 7}, Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(result.Skill.Files, want) {
		t.Fatalf("cloned update files = %#v, want %#v", result.Skill.Files, want)
	}
	if result.Skill.GitTreeSHA != changed.treeSHA("demo") {
		t.Fatalf("cloned update SHA = %q, want %q", result.Skill.GitTreeSHA, changed.treeSHA("demo"))
	}
	if result.Skill.Source.LastSeenRef != changed.commit {
		t.Fatalf("cloned update ref = %q, want %q", result.Skill.Source.LastSeenRef, changed.commit)
	}
}

func TestGitHubCheckUpdatesCancelDoesNotCompleteInFlightItem(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"legacy/SKILL.md":  {content: []byte(skillDocument("legacy", "old body")), mode: 0o644},
		"legacy/data.bin":  {content: []byte{1, 2, 3}, mode: 0o644},
		"sibling/SKILL.md": {content: []byte(skillDocument("sibling", "sibling body")), mode: 0o644},
		"sibling/notes.md": {content: []byte("notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openV1GitHubLibrary(t, fx.commit, []v1GitHubSkill{
		{name: "legacy", files: []library.File{
			{Path: "SKILL.md", Content: []byte(skillDocument("legacy", "old body")), Mode: library.ModeRegular},
			{Path: "data.bin", Content: []byte{1, 2, 3}, Mode: library.ModeRegular},
		}},
		{name: "sibling", files: []library.File{
			{Path: "SKILL.md", Content: []byte(skillDocument("sibling", "sibling body")), Mode: library.ModeRegular},
			{Path: "notes.md", Content: []byte("notes"), Mode: library.ModeRegular},
		}},
	})
	legacy := v1GitHubSkillByName(t, lib, "legacy")
	sibling := v1GitHubSkillByName(t, lib, "sibling")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transport := http.DefaultClient.Transport
	notesSHA := fx.blobSHA("sibling/notes.md")
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/git/blobs/"+notesSHA) {
			cancel()
			return nil, r.Context().Err()
		}
		return transport.RoundTrip(r)
	})
	results, err := lib.CheckUpdates(ctx, []library.SkillID{legacy.ID, sibling.ID})
	if !errors.Is(err, context.Canceled) || len(results) != 1 || results[0].SkillID != legacy.ID {
		t.Fatalf("canceled GitHub batch = %#v, %v", results, err)
	}
}

func TestURLCheckAndUpdateStillCompareFilesAndKeepEmptyGitTreeSHA(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"demo/SKILL.md": skillDocument("demo", "old"),
		"demo/run":      "echo old",
	})
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	spec := library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}}
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	if installed[0].GitTreeSHA != "" {
		t.Fatalf("URL GitTreeSHA = %q, want empty", installed[0].GitTreeSHA)
	}
	if available, err := lib.CheckUpdate(ctx, installed[0].ID); err != nil || available {
		t.Fatalf("unchanged URL CheckUpdate = %v, %v", available, err)
	}

	archive = zipArchive(t, map[string]string{
		"demo/SKILL.md": skillDocument("demo", "new"),
		"demo/run":      "echo new",
	})
	if available, err := lib.CheckUpdate(ctx, installed[0].ID); err != nil || !available {
		t.Fatalf("changed URL CheckUpdate = %v, %v", available, err)
	}
	result, err := lib.Update(ctx, installed[0].ID)
	if err != nil || !result.Changed || result.Skill.GitTreeSHA != "" {
		t.Fatalf("URL Update = %#v, %v", result, err)
	}
	if got := readPersistedGitTreeSHA(t, lib, installed[0].ID); got != "" {
		t.Fatalf("URL persisted GitTreeSHA = %q, want empty", got)
	}
}

func assertGitHubBlobPaths(t *testing.T, api *gitHubAPIMock, want ...string) {
	t.Helper()
	got := api.blobPaths()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blob paths = %v, want %v (%#v)", got, want, api.snapshot())
	}
}

func assertGitHubUpdateBlobs(t *testing.T, api *gitHubAPIMock, fx gitHubFixture, skillPath string) {
	t.Helper()
	allowed := map[string]bool{}
	for p, entry := range fx.paths {
		if entry.Type != "blob" {
			continue
		}
		if skillPath == "." || p == skillPath || strings.HasPrefix(p, skillPath+"/") {
			allowed[entry.SHA] = true
		}
	}
	var fetched []string
	for _, req := range api.snapshot() {
		sha, ok := strings.CutPrefix(req.path, "/repos/owner/repo/git/blobs/")
		if !ok {
			continue
		}
		filePath := ""
		for p, entry := range fx.paths {
			if entry.SHA == sha {
				filePath = p
				break
			}
		}
		if !allowed[sha] && filePath != "SKILL.md" && !strings.HasSuffix(filePath, "/SKILL.md") {
			t.Fatalf("Update fetched blob outside %s: %s (%v)", skillPath, sha, api.blobPaths())
		}
		fetched = append(fetched, sha)
	}
	if len(fetched) == 0 {
		t.Fatal("Update fetched no blobs")
	}
}

// assertGitHubCheckDoesNotFetchSkillFiles 适用于 commit 未命中、必须取 tree 比较目录 SHA 的检查:
// 取了 commit 与 tree,发现阶段至多读候选的 SKILL.md,不取任何其他 Skill 文件。commit 命中的场景用 assertGitHubCommitOnly。
func assertGitHubCheckDoesNotFetchSkillFiles(t *testing.T, api *gitHubAPIMock) {
	t.Helper()
	commits, trees, _ := gitHubAPIKindCounts(t, api)
	if commits == 0 || trees == 0 {
		t.Fatalf("missing commit or tree: %#v", api.snapshot())
	}
	for _, p := range api.blobPaths() {
		if p != "SKILL.md" && !strings.HasSuffix(p, "/SKILL.md") {
			t.Fatalf("check fetched skill file %s (%v)", p, api.blobPaths())
		}
	}
}

func gitHubAPIKindCounts(t *testing.T, api *gitHubAPIMock) (commits, trees, blobs int) {
	t.Helper()
	for _, req := range api.snapshot() {
		switch {
		case strings.HasPrefix(req.path, "/repos/owner/repo/commits/"):
			commits++
		case strings.HasPrefix(req.path, "/repos/owner/repo/git/trees/"):
			trees++
		case strings.HasPrefix(req.path, "/repos/owner/repo/git/blobs/"):
			blobs++
		default:
			t.Fatalf("unexpected API request %s", req.path)
		}
	}
	return commits, trees, blobs
}

type v1GitHubSkill struct {
	name  string
	files []library.File
}

func openV1GitHubLibrary(t *testing.T, commit string, skills []v1GitHubSkill) library.Library {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.db")
	writeV1LibraryFile(t, path)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	locator, err := json.Marshal(library.SourceLocator{Repository: "https://github.com/owner/repo.git", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sources(id, type, locator, last_fetched, last_seen_ref) VALUES (1, 'github', ?, '', ?)`, locator, commit); err != nil {
		t.Fatal(err)
	}
	for i, skill := range skills {
		skillID, versionID := i+1, i+1
		if _, err := db.Exec(`INSERT INTO skills(id, source_id, name) VALUES (?, 1, ?)`, skillID, skill.name); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO skill_versions(id, skill_id) VALUES (?, ?)`, versionID, skillID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, skillID); err != nil {
			t.Fatal(err)
		}
		for _, file := range skill.files {
			sum := sha256.Sum256(file.Content)
			fingerprint := hex.EncodeToString(sum[:])
			if _, err := db.Exec(`INSERT OR IGNORE INTO blobs(fingerprint, content) VALUES (?, ?)`, fingerprint, file.Content); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO version_files(version_id, path, fingerprint, mode) VALUES (?, ?, ?, ?)`, versionID, file.Path, fingerprint, file.Mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
}

func v1GitHubSkillByName(t *testing.T, lib library.Library, name string) library.Skill {
	t.Helper()
	summaries, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if summary.Name != name {
			continue
		}
		skill, err := lib.GetSkill(t.Context(), summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	t.Fatalf("skill %q not found", name)
	return library.Skill{}
}
