package library_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 接入方按 JSON 字段名与枚举值对接;术语改名后的字段与取值在这里逐一钉住。
func TestJSONFieldNamesAndValuesFollowTerminology(t *testing.T) {
	at := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"candidate fetched", library.Candidate{Fetched: 7, Name: "demo"}, `{"fetched":7,"name":"demo","description":"","path":""}`},
		{"skill never updated", library.Skill{VersionID: 3, Revision: "abc", FetchedAt: at}, `{"id":0,"name":"","description":"","source":{"id":0,"type":"","label":""},"version_id":3,"revision":"abc","fetched_at":"2026-09-27T00:00:00Z"}`},
		{"source display", library.Source{ID: 1, Type: library.SourceGitHub, Label: "owner/repo", Ref: "main", ViewURL: "https://github.com/owner/repo/tree/main"}, `{"id":1,"type":"github","label":"owner/repo","ref":"main","view_url":"https://github.com/owner/repo/tree/main"}`},
		{"group install created_at", library.GroupInstall{GroupID: 1, Scope: library.GlobalScope(library.AgentCodex), CreatedAt: at}, `{"group_id":1,"scope":{"kind":"global","agent":"codex"},"created_at":"2026-09-27T00:00:00Z"}`},
		{"remove skill folders", library.RemoveSkillOptions{Folders: library.Keep}, `{"folders":"keep"}`},
		{"unregister project folders", library.UnregisterProjectOptions{Folders: library.Remove}, `{"folders":"remove"}`},
		{"remove from group if_no_other_install", library.RemoveFromGroupOptions{IfNoOtherInstall: library.UninstallMember}, `{"if_no_other_install":"uninstall"}`},
		{"install plan already installed", library.InstallAlreadyInstalled, `"already_installed"`},
		{"member installed", library.MemberInstalled, `"installed"`},
		{"member already installed", library.MemberAlreadyInstalled, `"already_installed"`},
		{"reinstalled", library.Reinstalled, `"reinstalled"`},
		{"preview fetch github", library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo@demo", Ref: "main", Subpath: "skills"}}, `{"github":{"input":"owner/repo@demo","ref":"main","subpath":"skills"}}`},
		{"preview fetch url", library.PreviewFetchRequest{URL: "https://example.com/skills.zip"}, `{"url":"https://example.com/skills.zip"}`},
		{"preview fetch local", library.PreviewFetchRequest{Local: "/skills"}, `{"local":"/skills"}`},
		{"fetch preview ref and revision", library.FetchPreview{ID: "p", Ref: "main", Revision: "abc", Candidates: []library.Candidate{{Name: "demo"}}, Diagnostics: []library.PreviewDiagnostic{{Path: "bad", Reason: "missing SKILL.md"}}}, `{"id":"p","ref":"main","revision":"abc","candidates":[{"name":"demo","description":"","path":""}],"diagnostics":[{"path":"bad","reason":"missing SKILL.md"}]}`},
		{"fetch preview without ref", library.FetchPreview{ID: "p", Revision: "hash", Candidates: []library.Candidate{}}, `{"id":"p","revision":"hash","candidates":[]}`},
		{"search skill preview", library.SearchSkill{ID: "owner/repo/demo", Name: "demo", DetailURL: "https://skills.sh/owner/repo/demo", Preview: library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo@demo"}}}, `{"id":"owner/repo/demo","name":"demo","detail_url":"https://skills.sh/owner/repo/demo","preview":{"github":{"input":"owner/repo@demo"}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.value)
			if err != nil || string(got) != test.want {
				t.Fatalf("json.Marshal = %s, %v; want %s", got, err, test.want)
			}
		})
	}
}
