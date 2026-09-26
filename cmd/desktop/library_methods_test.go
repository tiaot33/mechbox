package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 桌面端要覆盖技能库的全部功能：Library 接口上的每个方法(Close 由服务生命周期负责)
// 都必须以同名、同签名开放给前端。
func TestLibraryServiceExposesEveryLibraryMethod(t *testing.T) {
	libType := reflect.TypeFor[library.Library]()
	serviceType := reflect.TypeFor[*LibraryService]()
	for i := range libType.NumMethod() {
		want := libType.Method(i)
		if want.Name == "Close" {
			continue
		}
		got, ok := serviceType.MethodByName(want.Name)
		if !ok {
			t.Errorf("LibraryService lacks %s", want.Name)
			continue
		}
		// 服务方法的第一个入参是接收者，去掉后与接口方法比较。
		if in, out := got.Type.NumIn()-1, got.Type.NumOut(); in != want.Type.NumIn() || out != want.Type.NumOut() {
			t.Errorf("%s signature = %v; want %v", want.Name, got.Type, want.Type)
			continue
		}
		for j := range want.Type.NumIn() {
			if got.Type.In(j+1) != want.Type.In(j) {
				t.Errorf("%s parameter %d = %v; want %v", want.Name, j, got.Type.In(j+1), want.Type.In(j))
			}
		}
		for j := range want.Type.NumOut() {
			if got.Type.Out(j) != want.Type.Out(j) {
				t.Errorf("%s result %d = %v; want %v", want.Name, j, got.Type.Out(j), want.Type.Out(j))
			}
		}
	}
}

// 经绑定服务走一遍主要写路径：安装本地技能、分组、登记项目、激活、巡检、重写、停用、移除。
func TestLibraryServiceWriteFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // 全局作用域目录由 Open 时的用户主目录算出
	ctx := t.Context()
	service := NewLibraryService(filepath.Join(t.TempDir(), "library.db"))
	if err := service.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = service.ServiceShutdown() })

	dir := filepath.Join(t.TempDir(), "demo")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: demo\ndescription: demo skill\n---\n"), 0o644))
	skill, err := service.Install(ctx, library.LocalSource{Path: dir})
	must(t, err)

	group, err := service.CreateGroup(ctx, "g")
	must(t, err)
	_, err = service.AddSkillToGroup(ctx, group.ID, skill.ID)
	must(t, err)
	group, err = service.RenameGroup(ctx, group.ID, "renamed")
	must(t, err)
	if group.Name != "renamed" {
		t.Fatalf("RenameGroup name = %q", group.Name)
	}

	projectDir := filepath.Join(t.TempDir(), "project")
	must(t, os.MkdirAll(projectDir, 0o755))
	project, err := service.RegisterProject(ctx, projectDir)
	must(t, err)

	global := library.GlobalScope(library.AgentClaudeCode)
	preview, err := service.PreviewActivate(ctx, skill.ID, global)
	must(t, err)
	if preview.Status != library.ActivateReady {
		t.Fatalf("PreviewActivate status = %q; want ready", preview.Status)
	}
	must(t, service.Activate(ctx, skill.ID, global, library.ActivateOptions{}))
	_, err = service.ActivateGroup(ctx, group.ID, library.ProjectScope(library.AgentCodex, project.ID), library.ActivateOptions{})
	must(t, err)

	inspections, err := service.InspectProjections(ctx, library.ProjectionFilter{})
	must(t, err)
	if len(inspections) != 2 {
		t.Fatalf("InspectProjections = %d projections; want 2", len(inspections))
	}
	results, err := service.RewriteProjections(ctx, library.ProjectionFilter{SkillID: skill.ID}, library.RewriteOptions{})
	must(t, err)
	if len(results) != 2 {
		t.Fatalf("RewriteProjections = %d results; want 2", len(results))
	}

	must(t, service.Deactivate(ctx, skill.ID, global))
	must(t, service.DeactivateGroup(ctx, group.ID, library.ProjectScope(library.AgentCodex, project.ID)))
	must(t, service.DeleteGroup(ctx, group.ID))
	must(t, service.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{}))
	must(t, service.RemoveSkill(ctx, skill.ID, library.RemoveSkillOptions{}))
	skills, err := service.ListSkills(ctx)
	must(t, err)
	if len(skills) != 0 {
		t.Fatalf("ListSkills after RemoveSkill = %d; want 0", len(skills))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
