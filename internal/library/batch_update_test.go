package library_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestCheckUpdatesReusesSourceAndReturnsIndependentOrderedResults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	archive := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "old"), "second/SKILL.md": skillDocument("second", "old")})
	requests := map[string]int{}
	failed := false
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		requests[r.URL.Path]++
		if failed && r.URL.Path == "/broken" {
			return nil, errors.New("offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	var skills []library.Skill
	for _, suffix := range []string{"/good", "/broken"} {
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: "https://source.example" + suffix})
		if err != nil {
			t.Fatal(err)
		}
		fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
		if err != nil {
			t.Fatal(err)
		}
		skills = append(skills, fetched...)
	}
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(t.Context(), skills[0].ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	installation, err := lib.ListInstallations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	archive = zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "new"), "second/SKILL.md": skillDocument("second", "old")})
	failed = true
	clear(requests)
	ids := []library.SkillID{skills[2].ID, skills[0].ID, 0, skills[1].ID, skills[0].ID, 99999, skills[3].ID}
	results, err := lib.CheckUpdates(t.Context(), ids)
	if err != nil || len(results) != 6 {
		t.Fatalf("batch = %#v, %v", results, err)
	}
	for i, id := range []library.SkillID{skills[2].ID, skills[0].ID, 0, skills[1].ID, 99999, skills[3].ID} {
		if results[i].SkillID != id {
			t.Fatalf("order = %#v", results)
		}
	}
	if !library.IsCode(results[0].Error, library.SourceUnreachable) || results[1].Error != nil || !results[1].Available || !library.IsCode(results[2].Error, library.InvalidArgument) || results[3].Error != nil || results[3].Available || !library.IsCode(results[4].Error, library.NotFound) || !library.IsCode(results[5].Error, library.SourceUnreachable) {
		t.Fatalf("results = %#v", results)
	}
	if requests["/good"] != 1 || requests["/broken"] != 1 {
		t.Fatalf("requests = %v", requests)
	}
	after, err := lib.ListSkills(t.Context())
	for i := range after {
		after[i].LastUpdateCheck = nil // 检查仅允许改变检查记录,原有技能与来源元数据保持不变。
	}
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("mutated Library = %#v, %v", after, err)
	}
	afterInstallations, err := lib.ListInstallations(t.Context())
	if err != nil || !reflect.DeepEqual(installation, afterInstallations) {
		t.Fatalf("mutated installations = %#v, %v", afterInstallations, err)
	}
	for _, original := range skills {
		got, err := lib.GetSkill(t.Context(), original.ID)
		if err != nil || !reflect.DeepEqual(got.Files, original.Files) {
			t.Fatalf("mutated content = %#v, %v", got, err)
		}
		versions, err := lib.ListVersions(t.Context(), original.ID)
		if err != nil || len(versions) != 1 {
			t.Fatalf("mutated history = %#v, %v", versions, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lib.CheckUpdates(ctx, ids); !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	if results, err := lib.CheckUpdates(t.Context(), nil); err != nil || len(results) != 0 {
		t.Fatalf("empty = %#v, %v", results, err)
	}
	archive = zipArchive(t, map[string]string{"second/SKILL.md": skillDocument("second", "old")})
	clear(requests)
	results, err = lib.CheckUpdates(t.Context(), []library.SkillID{skills[0].ID, skills[1].ID})
	if err != nil || len(results) != 2 || !library.IsCode(results[0].Error, library.NotFound) || results[1].Error != nil || results[1].Available || requests["/good"] != 1 {
		t.Fatalf("removed target = %#v, %v; requests %v", results, err, requests)
	}
}

func TestCheckUpdatesSeparatesRefsAndDetectsOnlyExecutableChanges(t *testing.T) {
	repository, _ := githubRemote(t)
	runGit(t, repository, "tag", "fixed")
	lib := openTestLibrary(t)
	var skills []library.Skill
	for _, ref := range []string{"trunk", "fixed"} {
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: ref}})
		if err != nil {
			t.Fatal(err)
		}
		fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
		if err != nil {
			t.Fatal(err)
		}
		skills = append(skills, fetched...)
	}
	local := filepath.Join(t.TempDir(), "local")
	writeLocalSkill(t, local, "local content")
	skill, err := fetchLocalDir(t.Context(), lib, local)
	if err != nil {
		t.Fatal(err)
	}
	skills = append(skills, skill)
	for _, root := range []string{repository, local} {
		if err := os.Chmod(filepath.Join(root, "SKILL.md"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	commitGit(t, repository, "executable mode")
	results, err := lib.CheckUpdates(t.Context(), []library.SkillID{skills[0].ID, skills[1].ID, skills[2].ID})
	if err != nil || len(results) != 3 {
		t.Fatalf("results = %#v, %v", results, err)
	}
	for i, want := range []bool{true, false, true} {
		if results[i].Error != nil || results[i].Available != want {
			t.Fatalf("ref and mode = %#v", results)
		}
	}
	for _, skill := range skills {
		if versions, err := lib.ListVersions(t.Context(), skill.ID); err != nil || len(versions) != 1 {
			t.Fatalf("history = %#v, %v", versions, err)
		}
	}
}

func TestCheckUpdatesCancellationReturnsOnlyCompletedItems(t *testing.T) {
	lib := openTestLibrary(t)
	archive := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "old"), "second/SKILL.md": skillDocument("second", "old")})
	spec := serveArchive(t, archive)
	first, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lib.Fetch(t.Context(), first.ID, candidateNames(first))
	if err != nil {
		t.Fatal(err)
	}
	spec.URL += "?second"
	second, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.Fetch(t.Context(), second.ID, candidateNames(second))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.RawQuery == "second" {
			cancel()
			return nil, r.Context().Err()
		}
		return transport.RoundTrip(r)
	})
	results, err := lib.CheckUpdates(ctx, []library.SkillID{a[0].ID, b[0].ID, a[1].ID, b[1].ID})
	if !errors.Is(err, context.Canceled) || len(results) != 2 || results[0].SkillID != a[0].ID || results[1].SkillID != a[1].ID {
		t.Fatalf("canceled batch = %#v, %v", results, err)
	}
	for _, skill := range a {
		if state := updateCheckState(t, lib, skill.ID); state == nil || state.Status != library.UpdateCurrent {
			t.Fatalf("completed check = %+v", state)
		}
	}
	for _, skill := range b {
		if state := updateCheckState(t, lib, skill.ID); state != nil {
			t.Fatalf("canceled check = %+v", state)
		}
	}
}
