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

func TestPlanInstallReadyAndInstalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "# Example")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanInstall(ctx, skill.ID, scope)
	if err != nil || plan.Status != library.InstallReady || plan.SkillID != skill.ID || plan.Name != skill.Name || plan.Directory != directory || plan.Target != filepath.Join(directory, skill.Name) {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan created directory: %v", err)
	}
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		if missing {
			if err := os.RemoveAll(plan.Target); err != nil {
				t.Fatal(err)
			}
		}
		got, err := lib.PlanInstall(ctx, skill.ID, scope)
		if err != nil || got.Status != library.InstallAlreadyInstalled {
			t.Fatalf("installed = %#v, %v", got, err)
		}
	}
}

func TestPlanInstallOccupancyAndUntracked(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetchSame := func() library.Skill {
		source := filepath.Join(t.TempDir(), "same")
		writeLocalSkill(t, source, "# Same")
		skill, err := fetchLocalDir(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	a, b := fetchSame(), fetchSame()
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, a.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanInstall(ctx, b.ID, scope)
	if err != nil || plan.Status != library.InstallOccupied || plan.OccupierSkillID != a.ID || plan.OccupierName != a.Name {
		t.Fatalf("occupied = %#v, %v", plan, err)
	}
	for _, policy := range []library.ConflictPolicy{"", library.Overwrite} {
		requireErrorDetails(t, lib.Install(ctx, b.ID, scope, library.InstallOptions{OnConflict: policy}), library.FailedPrecondition, library.ErrorDetails{SkillID: a.ID, Name: a.Name, Path: plan.Target})
	}
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	other := library.ProjectScope(library.AgentCodex, project.ID)
	plan, err = lib.PlanInstall(ctx, a.ID, other)
	if err != nil || plan.Status != library.InstallOccupied || plan.OccupierSkillID != a.ID || plan.OccupierName != a.Name {
		t.Fatalf("cross scope = %#v, %v", plan, err)
	}
	requireErrorDetails(t, lib.Install(ctx, a.ID, other, library.InstallOptions{}), library.FailedPrecondition, library.ErrorDetails{Path: plan.Directory})
	if err := lib.Uninstall(ctx, a.ID, scope); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"# Same", "user edited"} {
		writeFile(t, filepath.Join(plan.Target, "SKILL.md"), []byte(content), 0o644)
		got, err := lib.PlanInstall(ctx, b.ID, scope)
		if err != nil || got.Status != library.InstallUntracked || got.OccupierSkillID != 0 || got.OccupierName != "" {
			t.Fatalf("untracked = %#v, %v", got, err)
		}
		data, err := os.ReadFile(filepath.Join(got.Target, "SKILL.md"))
		if err != nil || string(data) != content {
			t.Fatalf("plan changed content: %q, %v", data, err)
		}
	}
	requireErrorDetails(t, lib.Install(ctx, b.ID, scope, library.InstallOptions{}), library.AlreadyExists, library.ErrorDetails{Path: plan.Target})
	if err := lib.Install(ctx, b.ID, scope, library.InstallOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanGroupInstallReportsDuplicatesAndMemberStates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	scope := library.GlobalScope(library.AgentCodex)
	group, err := lib.CreateGroup(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(empty.Members) != 0 || len(empty.DuplicateNames) != 0 {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
	for _, name := range []string{"same", "same", "ready"} {
		source := filepath.Join(t.TempDir(), name)
		writeLocalSkill(t, source, "# Skill")
		skill, err := fetchLocalDir(ctx, lib, source)
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
	if err := lib.Install(ctx, members[0].ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListSkillScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 3 || !reflect.DeepEqual(got.DuplicateNames, []string{"same"}) {
		t.Fatalf("group = %#v, %v", got, err)
	}
	// 同名且尚未安装的成员标为 duplicate(优先于它本来的 occupied),已安装的那个仍是 installed。
	for i, status := range []library.InstallPlanStatus{library.InstallAlreadyInstalled, library.InstallDuplicate, library.InstallReady} {
		if got.Members[i].SkillID != members[i].ID || got.Members[i].Status != status {
			t.Fatalf("member %d = %#v", i, got.Members[i])
		}
	}
	after, err := lib.ListSkillScopes(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("plan changed skill scopes: %#v, %v", after, err)
	}
	afterSkills, err := lib.ListGroupSkills(ctx, group.ID)
	if err != nil || !reflect.DeepEqual(members, afterSkills) {
		t.Fatalf("plan changed skills: %#v, %v", afterSkills, err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, group.ID, members[0].ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err = lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(got.DuplicateNames) != 0 || len(got.Members) != 2 || got.Members[0].Status != library.InstallOccupied || got.Members[1].Status != library.InstallReady {
		t.Fatalf("mixed = %#v, %v", got, err)
	}
}

// 安装计划标出的 duplicate 与安装时的跳过一致:两个同名成员都没安装时,即使有未记录文件夹也显示 duplicate,
// InstallGroup(Overwrite)也照样跳过它们、不覆盖该文件夹。
func TestPlanGroupInstallDuplicateMatchesGroupInstall(t *testing.T) {
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
		skill := fetchSkillWithBody(t, lib, "same", body)
		skills = append(skills, skill)
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(directory, "same", "manual.txt"), []byte("manual"), 0o644)
	got, err := lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 2 || got.Members[0].Status != library.InstallDuplicate || got.Members[1].Status != library.InstallDuplicate {
		t.Fatalf("PlanGroupInstall() = %#v, %v; want both duplicate", got, err)
	}
	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
	details := library.ErrorDetails{Name: "same", Path: filepath.Join(directory, "same")}
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(skills[0].ID, scope, library.FailedPrecondition, details),
		skippedMember(skills[1].ID, scope, library.FailedPrecondition, details),
	})
	if _, err := os.Stat(filepath.Join(directory, "same", "manual.txt")); err != nil {
		t.Fatalf("untracked folder was overwritten: %v", err)
	}
}

func TestInstallPlansReportMissingAndCanceled(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	group, err := lib.CreateGroup(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	if _, err := lib.PlanInstall(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown skill: %v", err)
	}
	if _, err := lib.PlanGroupInstall(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown group: %v", err)
	}
	if _, err := lib.PlanGroupInstall(ctx, group.ID, library.GlobalScope("missing")); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown scope: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.PlanInstall(canceled, -1, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled skill: %v", err)
	}
	if _, err := lib.PlanGroupInstall(canceled, group.ID, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled group: %v", err)
	}
}
