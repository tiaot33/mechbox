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

	"github.com/tiaot33/skills-tui/library"
)

func TestCheckUpdatesReusesSourceAndReturnsIndependentOrderedResults(t *testing.T) {
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
		fetch, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example" + suffix}})
		if err != nil {
			t.Fatal(err)
		}
		installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		skills = append(skills, installed...)
	}
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.Activate(t.Context(), skills[0].ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	activation, err := lib.ListActivations(t.Context())
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
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("mutated Library = %#v, %v", after, err)
	}
	afterActivation, err := lib.ListActivations(t.Context())
	if err != nil || !reflect.DeepEqual(activation, afterActivation) {
		t.Fatalf("mutated activations = %#v, %v", afterActivation, err)
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
	if _, err := lib.CheckUpdates(ctx, ids); !errors.Is(err, context.Canceled) {
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
		spec, err := lib.ResolveSource(t.Context(), "owner/repo", library.ResolveSourceOptions{Ref: ref})
		if err != nil {
			t.Fatal(err)
		}
		fetch, err := lib.Fetch(t.Context(), spec)
		if err != nil {
			t.Fatal(err)
		}
		installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		skills = append(skills, installed...)
	}
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "SKILL.md"), []byte("local content"), 0o644)
	skill, err := lib.Install(t.Context(), library.LocalSource{Path: local})
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
	first, err := lib.Fetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lib.InstallFetch(t.Context(), first.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec.Locator.URL += "?second"
	second, err := lib.Fetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.InstallFetch(t.Context(), second.ID, nil)
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
}
