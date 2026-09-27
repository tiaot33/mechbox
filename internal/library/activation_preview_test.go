package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestPreviewActivateReadyAndProjected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "# Example")
	skill, err := fetchLocal(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewActivate(ctx, skill.ID, scope)
	if err != nil || preview.Status != library.ActivateReady || preview.SkillID != skill.ID || preview.Name != skill.Name || preview.Directory != directory || preview.Target != filepath.Join(directory, skill.Name) {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview created directory: %v", err)
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		if missing {
			if err := os.RemoveAll(preview.Target); err != nil {
				t.Fatal(err)
			}
		}
		got, err := lib.PreviewActivate(ctx, skill.ID, scope)
		if err != nil || got.Status != library.ActivateProjected {
			t.Fatalf("projected = %#v, %v", got, err)
		}
	}
}

func TestPreviewActivateOccupancyAndUntracked(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	ctx := t.Context()
	install := func() library.Skill {
		source := filepath.Join(t.TempDir(), "same")
		writeLocalSkill(t, source, "# Same")
		skill, err := fetchLocal(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	a, b := install(), install()
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Activate(ctx, a.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewActivate(ctx, b.ID, scope)
	if err != nil || preview.Status != library.ActivateOccupied || preview.OccupierSkillID != a.ID || preview.OccupierName != a.Name {
		t.Fatalf("occupied = %#v, %v", preview, err)
	}
	for _, policy := range []library.ConflictPolicy{"", library.Overwrite} {
		requireErrorDetails(t, lib.Activate(ctx, b.ID, scope, library.ActivateOptions{OnConflict: policy}), library.FailedPrecondition, library.ErrorDetails{SkillID: a.ID, Name: a.Name, Path: preview.Target})
	}
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	other := library.ProjectScope(library.AgentCodex, project.ID)
	preview, err = lib.PreviewActivate(ctx, a.ID, other)
	if err != nil || preview.Status != library.ActivateOccupied || preview.OccupierSkillID != a.ID || preview.OccupierName != a.Name {
		t.Fatalf("cross scope = %#v, %v", preview, err)
	}
	requireErrorDetails(t, lib.Activate(ctx, a.ID, other, library.ActivateOptions{}), library.FailedPrecondition, library.ErrorDetails{Path: preview.Directory})
	if err := lib.Deactivate(ctx, a.ID, scope); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"# Same", "user edited"} {
		writeFile(t, filepath.Join(preview.Target, "SKILL.md"), []byte(content), 0o644)
		got, err := lib.PreviewActivate(ctx, b.ID, scope)
		if err != nil || got.Status != library.ActivateUntracked || got.OccupierSkillID != 0 || got.OccupierName != "" {
			t.Fatalf("untracked = %#v, %v", got, err)
		}
		data, err := os.ReadFile(filepath.Join(got.Target, "SKILL.md"))
		if err != nil || string(data) != content {
			t.Fatalf("preview changed content: %q, %v", data, err)
		}
	}
	requireErrorDetails(t, lib.Activate(ctx, b.ID, scope, library.ActivateOptions{}), library.AlreadyExists, library.ErrorDetails{Path: preview.Target})
	if err := lib.Activate(ctx, b.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewActivateGroupReportsDuplicatesAndMemberStates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	scope := library.GlobalScope(library.AgentCodex)
	group, err := lib.CreateGroup(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := lib.PreviewActivateGroup(ctx, group.ID, scope)
	if err != nil || len(empty.Members) != 0 || len(empty.DuplicateNames) != 0 {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
	for _, name := range []string{"same", "same", "ready"} {
		source := filepath.Join(t.TempDir(), name)
		writeLocalSkill(t, source, "# Skill")
		skill, err := fetchLocal(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	members, err := lib.ListGroupSkills(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, members[0].ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.PreviewActivateGroup(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 3 || !reflect.DeepEqual(got.DuplicateNames, []string{"same"}) {
		t.Fatalf("group = %#v, %v", got, err)
	}
	// 同名且尚未投影的成员标为 duplicate(优先于它本来的 occupied),已投影的那个仍是 projected。
	for i, status := range []library.ActivatePreviewStatus{library.ActivateProjected, library.ActivateDuplicate, library.ActivateReady} {
		if got.Members[i].SkillID != members[i].ID || got.Members[i].Status != status {
			t.Fatalf("member %d = %#v", i, got.Members[i])
		}
	}
	after, err := lib.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("preview changed projections: %#v, %v", after, err)
	}
	afterSkills, err := lib.ListGroupSkills(ctx, group.ID)
	if err != nil || !reflect.DeepEqual(members, afterSkills) {
		t.Fatalf("preview changed skills: %#v, %v", afterSkills, err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, group.ID, members[0].ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err = lib.PreviewActivateGroup(ctx, group.ID, scope)
	if err != nil || len(got.DuplicateNames) != 0 || len(got.Members) != 2 || got.Members[0].Status != library.ActivateOccupied || got.Members[1].Status != library.ActivateReady {
		t.Fatalf("mixed = %#v, %v", got, err)
	}
}

// 预检标出的 duplicate 与激活时的跳过一致:两个同名成员都没投影时,即使有未记录文件夹也显示 duplicate,
// ActivateGroup(Overwrite)也照样跳过它们、不覆盖该文件夹。
func TestPreviewActivateGroupDuplicateMatchesActivation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	var skills []library.Skill
	for _, body := range []string{"# one\n", "# two\n"} {
		skill := installSkillWithBody(t, lib, "same", body)
		skills = append(skills, skill)
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(directory, "same", "manual.txt"), []byte("manual"), 0o644)
	got, err := lib.PreviewActivateGroup(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 2 || got.Members[0].Status != library.ActivateDuplicate || got.Members[1].Status != library.ActivateDuplicate {
		t.Fatalf("PreviewActivateGroup() = %#v, %v; want both duplicate", got, err)
	}
	results, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	details := library.ErrorDetails{Name: "same", Path: filepath.Join(directory, "same")}
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(skills[0].ID, scope, library.FailedPrecondition, details),
		skippedMember(skills[1].ID, scope, library.FailedPrecondition, details),
	})
	if _, err := os.Stat(filepath.Join(directory, "same", "manual.txt")); err != nil {
		t.Fatalf("untracked folder was overwritten: %v", err)
	}
}

func TestActivationPreviewsReportMissingAndCanceled(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	group, err := lib.CreateGroup(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	if _, err := lib.PreviewActivate(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown skill: %v", err)
	}
	if _, err := lib.PreviewActivateGroup(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown group: %v", err)
	}
	if _, err := lib.PreviewActivateGroup(ctx, group.ID, library.GlobalScope("missing")); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown scope: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.PreviewActivate(canceled, -1, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled skill: %v", err)
	}
	if _, err := lib.PreviewActivateGroup(canceled, group.ID, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled group: %v", err)
	}
}
