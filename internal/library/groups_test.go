package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestGroupCanContainSkillsAcrossSourcesAndSkillsCanBelongToMultipleGroups(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "source-one")
	secondSource := filepath.Join(root, "source-two")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# One\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Two\n"), 0o644)
	lib := openTestLibrary(t)
	first, err := lib.Install(t.Context(), library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(t.Context(), library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	debugging, err := lib.CreateGroup(t.Context(), "frontend debugging")
	if err != nil {
		t.Fatal(err)
	}
	shipping, err := lib.CreateGroup(t.Context(), "shipping")
	if err != nil {
		t.Fatal(err)
	}

	for _, membership := range []struct {
		group library.Group
		skill library.Skill
	}{
		{debugging, first},
		{debugging, second},
		{shipping, first},
	} {
		if _, err := lib.AddSkillToGroup(t.Context(), membership.group.ID, membership.skill.ID); err != nil {
			t.Fatal(err)
		}
	}

	debuggingSkills, err := lib.ListGroupSkills(t.Context(), debugging.ID)
	if err != nil {
		t.Fatal(err)
	}
	shippingSkills, err := lib.ListGroupSkills(t.Context(), shipping.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summaryIDs(debuggingSkills), []library.SkillID{first.ID, second.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("debugging members = %v, want %v", got, want)
	}
	if got, want := summaryIDs(shippingSkills), []library.SkillID{first.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shipping members = %v, want %v", got, want)
	}
}

func TestGroupOperationsReturnNotFoundForUnknownIDs(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "known")
	if err != nil {
		t.Fatal(err)
	}
	unknownGroup := library.GroupID(9999)
	unknownSkill := library.SkillID(9999)
	scope := library.GlobalScope(library.AgentCodex)

	checks := []struct {
		name string
		err  error
	}{
		{"activate group", func() error {
			_, err := lib.ActivateGroup(ctx, unknownGroup, scope, library.ActivateOptions{})
			return err
		}()},
		{"deactivate group", lib.DeactivateGroup(ctx, unknownGroup, scope)},
		{"list group", func() error { _, err := lib.ListGroupSkills(ctx, unknownGroup); return err }()},
		{"add to unknown group", func() error {
			_, err := lib.AddSkillToGroup(ctx, unknownGroup, skill.ID)
			return err
		}()},
		{"add unknown skill", func() error {
			_, err := lib.AddSkillToGroup(ctx, group.ID, unknownSkill)
			return err
		}()},
	}
	for _, check := range checks {
		if !library.IsCode(check.err, library.NotFound) {
			t.Errorf("%s error = %v, want NotFound", check.name, check.err)
		}
	}
}

func TestGroupLifecycleKeepsSkillsAndProjectedCopies(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "group-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("group\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	groupOnlySource := filepath.Join(t.TempDir(), "group-only")
	writeFile(t, filepath.Join(groupOnlySource, "SKILL.md"), []byte("group only\n"), 0o644)
	groupOnly, err := lib.Install(ctx, library.LocalSource{Path: groupOnlySource})
	if err != nil {
		t.Fatal(err)
	}
	first, err := lib.CreateGroup(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.CreateGroup(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []library.Group{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.AddSkillToGroup(ctx, first.ID, groupOnly.ID); err != nil {
		t.Fatal(err)
	}
	renamed, err := lib.RenameGroup(ctx, first.ID, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	groups, err := lib.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := groups, []library.Group{renamed, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroups() = %#v, want %#v", got, want)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.ActivateGroup(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.ActivateGroup(ctx, second.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteGroup(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed projected copy: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("DeleteGroup removed skill: %v", err)
	}
	projections, err := lib.ListProjections(ctx)
	wantProjections := []library.Projection{
		{SkillID: skill.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{second.ID}},
		{SkillID: groupOnly.ID, Scope: scope, Directory: directory, Direct: true},
	}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(projections), wantProjections) {
		t.Fatalf("projections after DeleteGroup = %#v, %v; want %#v", projections, err, wantProjections)
	}
	if _, err := os.Stat(filepath.Join(directory, groupOnly.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed group-only projected copy: %v", err)
	}
	// 删组转为直接激活后,仍可按 Remove 策略连同投影一起移除。
	if err := lib.RemoveSkill(ctx, groupOnly.ID, library.RemoveSkillOptions{ProjectedCopies: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, groupOnly.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RemoveSkill(Remove) left projection: %v", err)
	}
	projections, err = lib.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(projections), wantProjections[:1]) {
		t.Fatalf("projections after RemoveSkill = %#v, %v; want %#v", projections, err, wantProjections[:1])
	}
	groups, err = lib.ListGroups(ctx)
	if err != nil || !reflect.DeepEqual(groups, []library.Group{second}) {
		t.Fatalf("groups after DeleteGroup = %#v, %v", groups, err)
	}
	if err := lib.DeleteGroup(ctx, first.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("second DeleteGroup error = %v, want NotFound", err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, second.ID, skill.ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	members, err := lib.ListGroupSkills(ctx, second.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("members after remove = %#v, %v", members, err)
	}
	if _, err := lib.RenameGroup(ctx, library.GroupID(9999), "missing"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("RenameGroup unknown error = %v", err)
	}
}

func TestCreateGroupMakesOccupiedNamesUniqueAndRecordsCreatedAt(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	start := time.Now().UTC()
	first, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	third, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	if first.Name != "a" || second.Name != "a#2" || third.Name != "a#3" {
		t.Fatalf("CreateGroup names = %q, %q, %q; want a, a#2, a#3", first.Name, second.Name, third.Name)
	}
	for _, group := range []library.Group{first, second, third} {
		if group.CreatedAt.Before(start) || group.CreatedAt.After(end) {
			t.Fatalf("CreateGroup CreatedAt = %v, want in [%v, %v]", group.CreatedAt, start, end)
		}
	}
	if err := lib.DeleteGroup(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	reused, err := lib.CreateGroup(ctx, "a")
	if err != nil || reused.Name != "a#2" {
		t.Fatalf("CreateGroup after deleting a#2 = %#v, %v; want name a#2", reused, err)
	}
	spaced, err := lib.CreateGroup(ctx, "  a  ")
	if err != nil || spaced.Name != "a#4" {
		t.Fatalf("CreateGroup trimmed occupied name = %#v, %v; want a#4", spaced, err)
	}
	listed, err := lib.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Group{first, third, reused, spaced}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("ListGroups() = %#v, want %#v", listed, want)
	}
}

func TestRenameGroupMakesOccupiedNamesUniqueAndKeepsOwnName(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	a, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.CreateGroup(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := lib.RenameGroup(ctx, b.ID, "a")
	if err != nil || renamed.ID != b.ID || renamed.Name != "a#2" || !renamed.CreatedAt.Equal(b.CreatedAt) {
		t.Fatalf("RenameGroup(b → a) = %#v, %v; want id %d name a#2 created_at %v", renamed, err, b.ID, b.CreatedAt)
	}
	same, err := lib.RenameGroup(ctx, a.ID, "a")
	if err != nil || same != a {
		t.Fatalf("RenameGroup(a → a) = %#v, %v; want %#v", same, err, a)
	}
	trimmed, err := lib.RenameGroup(ctx, a.ID, "  a  ")
	if err != nil || trimmed != a {
		t.Fatalf("RenameGroup(a → \"  a  \") = %#v, %v; want %#v", trimmed, err, a)
	}
}

func TestGroupValidationAndDuplicateMembershipCarryDetails(t *testing.T) {
	lib := openTestLibrary(t)
	_, err := lib.CreateGroup(t.Context(), " ")
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "name"})
	group, err := lib.CreateGroup(t.Context(), "named")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.RenameGroup(t.Context(), group.ID, ""); err == nil {
		t.Fatal("RenameGroup accepted a blank name")
	} else {
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "name"})
	}
	skill := installNamedSkill(t, lib, "member")
	if _, err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	_, err = lib.AddSkillToGroup(t.Context(), group.ID, skill.ID)
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: skill.ID})
}
