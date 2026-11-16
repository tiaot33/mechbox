package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 技能对 Claude Code、Codex 声明的设置

const deploySkillMD = `---
name: deploy
description: Deploy the app
disable-model-invocation: true
allowed-tools: Bash(git push *) Read
hooks:
  PreToolUse:
    - matcher: Bash
      hooks:
        - type: command
          command: ./scripts/check.sh
---
# deploy
`

const deployCodexMetadata = `policy:
  allow_implicit_invocation: false
dependencies:
  tools:
    - type: mcp
      value: github
      url: https://example.com/mcp
`

var deploySettings = &library.AgentSettings{
	ClaudeCode: &library.ClaudeCodeSettings{
		ModelInvocationDisabled: true,
		AllowedTools:            []string{"Bash(git push *)", "Read"},
		Hooks:                   []library.ClaudeCodeHook{{Event: "PreToolUse", Matcher: "Bash", Type: "command", Detail: "./scripts/check.sh"}},
	},
	Codex: &library.CodexSettings{
		ImplicitInvocationDisabled: true,
		Tools:                      []library.CodexTool{{Type: "mcp", Value: "github", URL: "https://example.com/mcp"}},
	},
}

func writeDeploySkill(t *testing.T, folder string) {
	t.Helper()
	writeFile(t, filepath.Join(folder, "SKILL.md"), []byte(deploySkillMD), 0o644)
	writeFile(t, filepath.Join(folder, "agents", "openai.yaml"), []byte(deployCodexMetadata), 0o644)
}

func TestAgentSettingsShownBeforeAndAfterFetch(t *testing.T) {
	t.Parallel()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "deploy")
	writeDeploySkill(t, source)

	preview, err := lib.PreviewFetch(t.Context(), localRequest(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 1 || !reflect.DeepEqual(preview.Candidates[0].AgentSettings, deploySettings) {
		t.Fatalf("candidate settings = %#v, want %#v", preview.Candidates, deploySettings)
	}
	fetched := fetchSkills(t, lib, preview, candidateNames(preview))
	if !reflect.DeepEqual(fetched[0].AgentSettings, deploySettings) {
		t.Fatalf("fetched skill settings = %#v, want %#v", fetched[0].AgentSettings, deploySettings)
	}
	listed, err := lib.ListSkills(t.Context())
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0].AgentSettings, deploySettings) {
		t.Fatalf("ListSkills() = %#v, %v, want the settings", listed, err)
	}

	// 只写了开放规范字段的技能没有设置。
	plain := fetchNamedSkill(t, lib, "plain")
	if plain.AgentSettings != nil {
		t.Fatalf("plain skill settings = %#v, want nil", plain.AgentSettings)
	}
}

func TestAgentSettingsOfUnmanagedSkillsComeFromTheFolder(t *testing.T) {
	t.Parallel()
	lib := openTestLibrary(t)
	writeDeploySkill(t, filepath.Join(scopeDirectory(t, lib, library.GlobalScope(library.AgentCodex)), "deploy"))
	writeUnmanagedSkill(t, scopeDirectory(t, lib, library.GlobalScope(library.AgentClaudeCode)), "plain", "plain")

	skills, err := lib.ListUnmanagedSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*library.AgentSettings{}
	for _, skill := range skills {
		got[skill.Name] = skill.AgentSettings
	}
	if want := map[string]*library.AgentSettings{"deploy": deploySettings, "plain": nil}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unmanaged settings = %#v, want %#v", got, want)
	}
}

func TestUpdatePlanShowsLooserAgentSettingsAsRisks(t *testing.T) {
	t.Parallel()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "deploy")
	writeDeploySkill(t, source)
	skill, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte(`---
name: deploy
description: Deploy the app
allowed-tools: Bash(*), Read
hooks:
  PreToolUse:
    - matcher: Bash
      hooks:
        - type: command
          command: ./scripts/check.sh --fix
---
# deploy
`), 0o644)
	writeFile(t, filepath.Join(source, "agents", "openai.yaml"), []byte(`dependencies:
  tools:
    - type: mcp
      value: github
      url: https://example.com/mcp
    - type: mcp
      value: shell
      command: npx shell-server
`), 0o644)

	plan, err := lib.PlanUpdate(t.Context(), skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.UpdateRisk{
		{Kind: library.RiskAllowedToolsAdded, Path: "SKILL.md", Agent: library.AgentClaudeCode, Items: []string{"Bash(*)"}},
		{Kind: library.RiskHooksChanged, Path: "SKILL.md", Agent: library.AgentClaudeCode, Items: []string{"PreToolUse (Bash): ./scripts/check.sh --fix"}},
		{Kind: library.RiskAutoInvocationEnabled, Path: "SKILL.md", Agent: library.AgentClaudeCode},
		{Kind: library.RiskToolsAdded, Path: "agents/openai.yaml", Agent: library.AgentCodex, Items: []string{"shell: npx shell-server"}},
		{Kind: library.RiskAutoInvocationEnabled, Path: "agents/openai.yaml", Agent: library.AgentCodex},
	}
	if len(plan.Skills) != 1 || !reflect.DeepEqual(plan.Skills[0].Risks, want) {
		t.Fatalf("risks = %#v, want %#v", plan.Skills, want)
	}

	// 收紧(拿掉工具、hooks,关掉自动调用)不算风险。
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: deploy\ndescription: Deploy the app\ndisable-model-invocation: true\nallowed-tools: Read\n---\n# deploy\n"), 0o644)
	if err := os.Remove(filepath.Join(source, "agents", "openai.yaml")); err != nil {
		t.Fatal(err)
	}
	plan, err = lib.PlanUpdate(t.Context(), skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skills) != 1 || plan.Skills[0].Change != library.PlannedChanged || plan.Skills[0].Risks != nil {
		t.Fatalf("tightened plan = %#v, want a change without risks", plan.Skills)
	}
}
