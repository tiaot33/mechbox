package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
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
		if err := lib.AddSkillToGroup(t.Context(), membership.group.ID, membership.skill.ID); err != nil {
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
	scope := library.GlobalScope{Agent: library.AgentCodex}

	checks := []struct {
		name string
		err  error
	}{
		{"activate group", lib.ActivateGroup(ctx, unknownGroup, scope, library.ActivateOptions{})},
		{"deactivate group", lib.DeactivateGroup(ctx, unknownGroup, scope)},
		{"list group", func() error { _, err := lib.ListGroupSkills(ctx, unknownGroup); return err }()},
		{"add to unknown group", lib.AddSkillToGroup(ctx, unknownGroup, skill.ID)},
		{"add unknown skill", lib.AddSkillToGroup(ctx, group.ID, unknownSkill)},
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
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.AddSkillToGroup(ctx, first.ID, groupOnly.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.RenameGroup(ctx, first.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	groups, err := lib.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := groups, []library.Group{{ID: first.ID, Name: "renamed"}, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroups() = %#v, want %#v", got, want)
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, second.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
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
	activations, err := lib.ListActivations(ctx)
	wantActivations := []library.Activation{
		{SkillID: skill.ID, Scope: scope, Directory: directory, ViaGroups: []library.GroupID{second.ID}},
		{SkillID: groupOnly.ID, Scope: scope, Directory: directory, Individual: true},
	}
	if err != nil || !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("activations after DeleteGroup = %#v, %v; want %#v", activations, err, wantActivations)
	}
	if _, err := os.Stat(filepath.Join(directory, groupOnly.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed group-only projected copy: %v", err)
	}
	groups, err = lib.ListGroups(ctx)
	if err != nil || !reflect.DeepEqual(groups, []library.Group{second}) {
		t.Fatalf("groups after DeleteGroup = %#v, %v", groups, err)
	}
	if err := lib.DeleteGroup(ctx, first.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("second DeleteGroup error = %v, want NotFound", err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, second.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	members, err := lib.ListGroupSkills(ctx, second.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("members after remove = %#v, %v", members, err)
	}
	if err := lib.RenameGroup(ctx, library.GroupID(9999), "missing"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("RenameGroup unknown error = %v", err)
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
	if err := lib.RenameGroup(t.Context(), group.ID, ""); err == nil {
		t.Fatal("RenameGroup accepted a blank name")
	} else {
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "name"})
	}
	skill := installNamedSkill(t, lib, "member")
	if err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	requireErrorDetails(t, lib.AddSkillToGroup(t.Context(), group.ID, skill.ID), library.AlreadyExists, library.ErrorDetails{SkillID: skill.ID})
}
