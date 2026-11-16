package agentsettings

import (
	"reflect"
	"testing"
)

func TestParseClaudeReadsFrontmatterFields(t *testing.T) {
	t.Parallel()
	got, ok := ParseClaude([]byte(`---
name: commit
description: Commit changes
disable-model-invocation: Yes
user-invocable: off
allowed-tools: Bash(git add *) Bash(git commit *), Read
disallowed-tools:
  - AskUserQuestion
paths: "src/**/*.go, docs/*.md"
when_to_use: when committing
argument-hint: "[message]"
model: inherit
effort: high
context: fork
agent: Explore
shell: PowerShell
hooks:
  PreToolUse:
    - matcher: "Bash"
      hooks:
        - type: command
          command: ./scripts/check.sh
          args: ["--strict"]
        - type: http
          url: https://example.com/hook
  Stop:
    - hooks:
        - type: mcp_tool
          server: memory
          tool: save
---
# Commit
`))
	if !ok {
		t.Fatal("ParseClaude reported no settings")
	}
	want := Claude{
		ModelInvocationDisabled: true,
		UserInvocationDisabled:  true,
		AllowedTools:            []string{"Bash(git add *)", "Bash(git commit *)", "Read"},
		DisallowedTools:         []string{"AskUserQuestion"},
		Paths:                   []string{"src/**/*.go", "docs/*.md"},
		WhenToUse:               "when committing",
		ArgumentHint:            "[message]",
		Effort:                  "high",
		Fork:                    true,
		Agent:                   "Explore",
		Shell:                   "powershell",
		Hooks: []Hook{
			{Event: "PreToolUse", Matcher: "Bash", Type: "command", Detail: "./scripts/check.sh --strict"},
			{Event: "PreToolUse", Matcher: "Bash", Type: "http", Detail: "https://example.com/hook"},
			{Event: "Stop", Type: "mcp_tool", Detail: "memory/save"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseClaude = %+v, want %+v", got, want)
	}
}

func TestParseClaudeIgnoresDefaultsAndBrokenFrontmatter(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"only spec fields":    "---\nname: a\ndescription: b\nlicense: MIT\n---\n",
		"defaults":            "---\nname: a\ndescription: b\ndisable-model-invocation: false\nuser-invocable: true\nmodel: inherit\nshell: bash\ncontext: inline\nagent: Explore\n---\n",
		"not on first line":   "\n---\ndisable-model-invocation: true\n---\n",
		"unclosed":            "---\ndisable-model-invocation: true\n",
		"invalid yaml":        "---\ndisable-model-invocation: [true\n---\n",
		"unknown boolean":     "---\ndisable-model-invocation: maybe\n---\n",
		"hooks not a mapping": "---\nhooks: ./run.sh\n---\n",
	} {
		if got, ok := ParseClaude([]byte(content)); ok {
			t.Errorf("%s: ParseClaude = %+v, want no settings", name, got)
		}
	}
}

func TestParseCodexReadsPolicyAndTools(t *testing.T) {
	t.Parallel()
	got, ok := ParseCodex([]byte(`interface:
  display_name: "Deploy"
policy:
  allow_implicit_invocation: false
  products: [codex, CHATGPT, codex]
dependencies:
  tools:
    - type: "mcp"
      value: "  github  "
      description: "GitHub MCP"
      transport: "streamable_http"
      url: "https://example.com/mcp"
    - type: "mcp"
      value: "local"
      command: "npx   server"
    - type: "mcp"
    - value: "no type"
    - type: ""
      value: "empty type"
`))
	if !ok {
		t.Fatal("ParseCodex reported no settings")
	}
	want := Codex{
		ImplicitInvocationDisabled: true,
		Products:                   []string{"codex", "chatgpt"},
		Tools: []Tool{
			{Type: "mcp", Value: "github", Description: "GitHub MCP", Transport: "streamable_http", URL: "https://example.com/mcp"},
			{Type: "mcp", Value: "local", Command: "npx server"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCodex = %+v, want %+v", got, want)
	}
}

func TestParseCodexIgnoresWholeFileWhenCodexWould(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"empty":            "",
		"only interface":   "interface:\n  display_name: X\n",
		"implicit allowed": "policy:\n  allow_implicit_invocation: true\n",
		"invalid yaml":     "policy: [\n",
		"unknown product":  "policy:\n  allow_implicit_invocation: false\n  products: [ChatGPT]\n",
		"not a boolean":    "policy:\n  allow_implicit_invocation: sometimes\n",
	} {
		if got, ok := ParseCodex([]byte(content)); ok {
			t.Errorf("%s: ParseCodex = %+v, want no settings", name, got)
		}
	}
}
