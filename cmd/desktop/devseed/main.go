package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library"
)

func main() {
	out := flag.String("out", ".dev/seed", "")
	data := flag.String("data", "testdata/dev", "")
	flag.Parse()
	if runtime.GOOS == "windows" {
		log.Fatal("devseed runs on macOS and Linux only: on Windows the library takes the home directory from USERPROFILE, so a separate HOME does not isolate the agent directories")
	}
	s, err := newSeeder(*out, *data)
	if err != nil {
		log.Fatal(err)
	}
	if err := s.run(context.Background()); err != nil {
		if removeErr := os.RemoveAll(s.out); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		log.Fatal(err)
	}
}

type seeder struct {
	out, data, home string
	lib             *library.Library
	skills          map[string]library.SkillID
}

type gitSource struct {
	name, input, dir string
}

var (
	claude = library.GlobalScope(library.AgentClaudeCode)
	codex  = library.GlobalScope(library.AgentCodex)
)

func newSeeder(out, data string) (*seeder, error) {
	out, err := filepath.Abs(out)
	if err != nil {
		return nil, err
	}
	data, err = filepath.Abs(data)
	if err != nil {
		return nil, err
	}
	return &seeder{out: out, data: data, home: filepath.Join(out, "home"), skills: map[string]library.SkillID{}}, nil
}

func (s *seeder) snapshot(source, commit, skill string) string {
	return filepath.Join(s.data, "git", source, commit, "skills", skill)
}

func (s *seeder) run(ctx context.Context) error {
	if err := os.RemoveAll(s.out); err != nil {
		return err
	}
	for _, dir := range []string{".claude", ".codex", "code"} {
		if err := os.MkdirAll(filepath.Join(s.home, dir), 0o755); err != nil {
			return err
		}
	}
	if err := os.Setenv("HOME", s.home); err != nil {
		return err
	}
	os.Unsetenv("XDG_STATE_HOME")
	os.Unsetenv("XDG_CONFIG_HOME")
	if err := writeFile(filepath.Join(s.home, ".gitconfig"), "[user]\n\tname = Mechbox Dev\n\temail = dev@example.com\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"); err != nil {
		return err
	}

	anthropics, err := s.gitSource("anthropics-skills", "https://git.dev.test/anthropics/skills.git")
	if err != nil {
		return err
	}
	openai, err := s.gitSource("openai-skills", "https://git.dev.test/openai/skills.git")
	if err != nil {
		return err
	}
	if err := s.commit(anthropics, "89dcaa3", "Add claude-academy-guide skill (#1554)"); err != nil {
		return err
	}
	if err := s.commit(openai, "e6afb0d", "Fix imagegen skill placement under .system (#348)"); err != nil {
		return err
	}
	webApp := filepath.Join(s.home, "code", "web-app")
	oldProject := filepath.Join(s.home, "code", "old-project")
	if err := s.layoutHome(webApp, oldProject); err != nil {
		return err
	}

	lib, err := library.Open(filepath.Join(s.out, "library.db"))
	if err != nil {
		return err
	}
	defer lib.Close()
	s.lib = lib
	if err := s.requireIsolated(ctx); err != nil {
		return err
	}
	return s.populate(ctx, anthropics, openai, webApp, oldProject)
}

func (s *seeder) layoutHome(webApp, oldProject string) error {
	mySkills := filepath.Join(s.home, "code", "my-skills")
	copies := []struct{ from, to string }{
		{s.snapshot("openai-skills", "b0401f0", ".curated/gh-fix-ci"), filepath.Join(mySkills, "gh-fix-ci")},
		{s.snapshot("openai-skills", "b0401f0", ".curated/screenshot"), filepath.Join(mySkills, "screenshot")},
		{s.snapshot("anthropics-skills", "dbd4588", "internal-comms"), filepath.Join(s.home, ".claude", "skills", "internal-comms")},
		{s.snapshot("anthropics-skills", "dbd4588", "webapp-testing"), filepath.Join(s.home, ".agents", "skills", "webapp-testing")},
		{s.snapshot("openai-skills", "b0401f0", ".curated/playwright"), filepath.Join(webApp, ".claude", "skills", "playwright")},
	}
	for _, c := range copies {
		if err := os.CopyFS(c.to, os.DirFS(c.from)); err != nil {
			return err
		}
	}
	if err := os.Symlink("../../.agents/skills/webapp-testing", filepath.Join(s.home, ".claude", "skills", "webapp-testing")); err != nil {
		return err
	}
	files := []struct{ path, content string }{
		{filepath.Join(s.home, ".agents", ".skill-lock.json"), `{"version":3,"skills":{"webapp-testing":{"source":"anthropics/skills","sourceType":"git","sourceUrl":"https://git.dev.test/anthropics/skills.git","ref":"main"}}}` + "\n"},
		{filepath.Join(webApp, "README.md"), "# web-app\n"},
		{filepath.Join(oldProject, "README.md"), "# old-project\n"},
	}
	for _, f := range files {
		if err := writeFile(f.path, f.content); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		if err := git(webApp, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) requireIsolated(ctx context.Context) error {
	for _, scope := range []library.Scope{claude, codex} {
		dir, err := s.lib.ScopeDirectory(ctx, scope)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(dir, s.home+string(filepath.Separator)) {
			return fmt.Errorf("%s global directory %s is outside %s: the library does not use the seeded HOME", scope.Agent, dir, s.home)
		}
	}
	return nil
}

func (s *seeder) populate(ctx context.Context, anthropics, openai gitSource, webApp, oldProject string) error {
	settings, err := s.lib.AppSettings(ctx)
	if err != nil {
		return err
	}
	settings["first_open_done"] = "true"
	if err := s.lib.SetAppSettings(ctx, settings); err != nil {
		return err
	}

	if err := s.fetch(ctx, anthropics.input, "frontend-design", "brand-guidelines", "claude-academy-guide"); err != nil {
		return err
	}
	if err := s.fetch(ctx, openai.input, "openai-docs", "playwright", "sora", "doc"); err != nil {
		return err
	}
	if err := s.fetch(ctx, filepath.Join(s.home, "code", "my-skills"), "gh-fix-ci", "screenshot"); err != nil {
		return err
	}
	if _, err := s.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: claude, Name: "internal-comms"}); err != nil {
		return err
	}

	web, err := s.lib.RegisterProject(ctx, webApp)
	if err != nil {
		return err
	}
	old, err := s.lib.RegisterProject(ctx, oldProject)
	if err != nil {
		return err
	}
	installs := []struct {
		skill string
		scope library.Scope
	}{
		{"frontend-design", claude},
		{"frontend-design", codex},
		{"claude-academy-guide", claude},
		{"openai-docs", codex},
		{"playwright", claude},
		{"sora", claude},
		{"gh-fix-ci", library.ProjectScope(library.AgentClaudeCode, web.ID)},
		{"brand-guidelines", library.ProjectScope(library.AgentClaudeCode, old.ID)},
	}
	for _, install := range installs {
		if err := s.lib.Install(ctx, s.skills[install.skill], install.scope, library.InstallOptions{}); err != nil {
			return fmt.Errorf("install %s: %w", install.skill, err)
		}
	}
	if err := s.group(ctx, "前端", &codex, "frontend-design", "playwright", "brand-guidelines"); err != nil {
		return err
	}
	if err := s.group(ctx, "文档", nil, "doc", "openai-docs", "screenshot"); err != nil {
		return err
	}

	if err := s.commit(openai, "30444ae", "Align curated OpenAI Docs skill with system copy (#365)"); err != nil {
		return err
	}
	if err := s.update(ctx, s.skills["openai-docs"]); err != nil {
		return err
	}
	if err := s.commit(openai, "b0401f0", "[codex] Add define-goal skill (#417)"); err != nil {
		return err
	}
	if err := s.commit(anthropics, "dbd4588", "Update claude-api skill: dynamic workflows and the four workflow quickstarts (#1995)"); err != nil {
		return err
	}

	claudeSkills, err := s.lib.ScopeDirectory(ctx, claude)
	if err != nil {
		return err
	}
	if err := appendFile(filepath.Join(claudeSkills, "playwright", "SKILL.md"), "\n## 本机备注\n\n用 `npx playwright test --headed` 看浏览器操作。\n"); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(claudeSkills, "sora")); err != nil {
		return err
	}
	if err := os.RemoveAll(oldProject); err != nil {
		return err
	}

	collections, err := s.lib.ListCollections(ctx)
	if err != nil {
		return err
	}
	ids := make([]library.CollectionID, len(collections))
	for i, collection := range collections {
		ids[i] = collection.ID
	}
	_, err = s.lib.CheckUpdates(ctx, ids)
	return err
}

func (s *seeder) fetch(ctx context.Context, input string, names ...string) error {
	preview, err := s.lib.PreviewFetch(ctx, library.PreviewFetchRequest{Input: input})
	if err != nil {
		return fmt.Errorf("preview %s: %w", input, err)
	}
	skills, err := s.lib.Fetch(ctx, preview.ID, names)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", input, err)
	}
	for _, skill := range skills {
		s.skills[skill.Name] = skill.ID
	}
	return nil
}

func (s *seeder) group(ctx context.Context, name string, scope *library.Scope, skills ...string) error {
	group, err := s.lib.CreateGroup(ctx, name)
	if err != nil {
		return err
	}
	for _, skill := range skills {
		if _, err := s.lib.AddSkillToGroup(ctx, group.ID, s.skills[skill]); err != nil {
			return err
		}
	}
	if scope == nil {
		return nil
	}
	_, err = s.lib.InstallGroup(ctx, group.ID, *scope, library.InstallOptions{})
	return err
}

func (s *seeder) update(ctx context.Context, skill library.SkillID) error {
	current, err := s.lib.GetSkill(ctx, skill)
	if err != nil {
		return err
	}
	plan, err := s.lib.PlanUpdate(ctx, current.CollectionID)
	if err != nil {
		return err
	}
	var changed []library.SkillID
	for _, planned := range plan.Skills {
		if planned.Change == library.PlannedChanged {
			changed = append(changed, planned.Skill.ID)
		}
	}
	var installs []library.ReinstallRequest
	for _, install := range plan.Installs {
		if slices.Contains(changed, install.SkillID) {
			installs = append(installs, library.ReinstallRequest{SkillID: install.SkillID, Scope: install.Scope})
		}
	}
	_, err = s.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: changed, Installs: installs})
	return err
}

func (s *seeder) gitSource(name, input string) (gitSource, error) {
	source := gitSource{name: name, input: input, dir: filepath.Join(s.out, "git", name)}
	if err := os.MkdirAll(source.dir, 0o755); err != nil {
		return source, err
	}
	if err := git(source.dir, "init", "-q", "-b", "main"); err != nil {
		return source, err
	}
	return source, git(s.home, "config", "--global", "url.file://"+source.dir+".insteadOf", input)
}

func (s *seeder) commit(source gitSource, snapshot, message string) error {
	entries, err := os.ReadDir(source.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(source.dir, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.CopyFS(source.dir, os.DirFS(filepath.Join(s.data, "git", source.name, snapshot))); err != nil {
		return err
	}
	if err := git(source.dir, "add", "-A"); err != nil {
		return err
	}
	return git(source.dir, "commit", "-qm", message)
}

func git(dir string, args ...string) error {
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, output)
	}
	return nil
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func appendFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
