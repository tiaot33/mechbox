package library_test

import (
	"encoding/json"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// Scope 以 {"kind","agent","project_id"?} 往返 JSON:读出来的就是传回去的,没有目录字段。
func TestScopeJSONRoundTrip(t *testing.T) {
	cases := map[string]struct {
		scope library.Scope
		json  string
	}{
		"global":  {library.GlobalScope(library.AgentCodex), `{"kind":"global","agent":"codex"}`},
		"project": {library.ProjectScope(library.AgentClaudeCode, 7), `{"kind":"project","agent":"claude-code","project_id":7}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(c.scope)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != c.json {
				t.Fatalf("Marshal(%#v) = %s, want %s", c.scope, encoded, c.json)
			}
			var decoded library.Scope
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded != c.scope {
				t.Fatalf("Unmarshal(%s) = %#v, want %#v", encoded, decoded, c.scope)
			}
		})
	}
}

// ListActivations 读出的 Scope 经 JSON 往返后仍能原样传回 Deactivate。
func TestActivationScopeRoundTripsThroughJSONIntoDeactivate(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "demo")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, skill.ID, library.ProjectScope(library.AgentCodex, project.ID), library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != 1 {
		t.Fatalf("ListActivations() = %#v, %v; want one activation", activations, err)
	}
	encoded, err := json.Marshal(activations[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded library.Activation
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := lib.Deactivate(ctx, decoded.SkillID, decoded.Scope); err != nil {
		t.Fatalf("Deactivate(decoded scope %#v) = %v", decoded.Scope, err)
	}
	if activations, err := lib.ListActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after Deactivate = %#v, %v; want none", activations, err)
	}
}

// 每个接收 Scope 的方法在入口拒绝非法组合,Details.Field 指向出错的字段;不触及技能或分组是否存在。
func TestInvalidScopeIsInvalidArgumentOnEveryScopeMethod(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)

	scopes := map[string]struct {
		scope library.Scope
		field string
	}{
		"zero value":                {library.Scope{}, "kind"},
		"unknown kind":              {library.Scope{Kind: "workspace", Agent: library.AgentCodex}, "kind"},
		"global with a project":     {library.Scope{Kind: library.ScopeGlobal, Agent: library.AgentCodex, ProjectID: 1}, "project_id"},
		"project without a project": {library.Scope{Kind: library.ScopeProject, Agent: library.AgentCodex}, "project_id"},
		"project with negative id":  {library.Scope{Kind: library.ScopeProject, Agent: library.AgentCodex, ProjectID: -1}, "project_id"},
	}
	methods := map[string]func(library.Scope) error{
		"Resolve": func(scope library.Scope) error {
			_, err := lib.Resolve(ctx, scope)
			return err
		},
		"PreviewActivate": func(scope library.Scope) error {
			_, err := lib.PreviewActivate(ctx, 1, scope)
			return err
		},
		"Activate": func(scope library.Scope) error {
			return lib.Activate(ctx, 1, scope, library.ActivateOptions{})
		},
		"Deactivate": func(scope library.Scope) error {
			return lib.Deactivate(ctx, 1, scope)
		},
		"PreviewActivateGroup": func(scope library.Scope) error {
			_, err := lib.PreviewActivateGroup(ctx, 1, scope)
			return err
		},
		"ActivateGroup": func(scope library.Scope) error {
			return lib.ActivateGroup(ctx, 1, scope, library.ActivateOptions{})
		},
		"DeactivateGroup": func(scope library.Scope) error {
			return lib.DeactivateGroup(ctx, 1, scope)
		},
	}
	for method, call := range methods {
		for name, c := range scopes {
			t.Run(method+"/"+name, func(t *testing.T) {
				requireErrorDetails(t, call(c.scope), library.InvalidArgument, library.ErrorDetails{Field: c.field})
			})
		}
	}
}
