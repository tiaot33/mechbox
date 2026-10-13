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
		{"skill never updated", library.Skill{CollectionID: 2, VersionID: 3, Revision: "abc", FetchedAt: at}, `{"id":0,"name":"","description":"","collection_id":2,"source":{"type":"","label":""},"version_id":3,"revision":"abc","fetched_at":"2026-09-27T00:00:00Z"}`},
		{"source display", library.Source{Type: library.SourceGitHub, Label: "owner/repo", Ref: "main", ViewURL: "https://github.com/owner/repo/tree/main"}, `{"type":"github","label":"owner/repo","ref":"main","view_url":"https://github.com/owner/repo/tree/main"}`},
		{"collection skill ids", library.Collection{ID: 2, Source: library.Source{Type: library.SourceLocal, Label: "/skills"}, Skills: []library.SkillID{4, 5}}, `{"id":2,"source":{"type":"local","label":"/skills"},"skills":[4,5]}`},
		{"group members", library.Group{ID: 1, Name: "debugging", CreatedAt: at, Members: []library.SkillID{4}}, `{"id":1,"name":"debugging","created_at":"2026-09-27T00:00:00Z","members":[4]}`},
		{"empty group members", library.Group{ID: 1, Name: "empty", Members: []library.SkillID{}}, `{"id":1,"name":"empty","members":[]}`},
		{"group install created_at", library.GroupInstall{GroupID: 1, Scope: library.GlobalScope(library.AgentCodex), CreatedAt: at}, `{"group_id":1,"scope":{"kind":"global","agent":"codex"},"created_at":"2026-09-27T00:00:00Z"}`},
		{"remove skill folders", library.RemoveSkillOptions{Folders: library.Keep}, `{"folders":"keep"}`},
		{"unregister project folders", library.UnregisterProjectOptions{Folders: library.Remove}, `{"folders":"remove"}`},
		{"remove from group if_no_other_install", library.RemoveFromGroupOptions{IfNoOtherInstall: library.UninstallMember}, `{"if_no_other_install":"uninstall"}`},
		{"install plan already installed", library.InstallAlreadyInstalled, `"already_installed"`},
		{"member installed", library.MemberInstalled, `"installed"`},
		{"member already installed", library.MemberAlreadyInstalled, `"already_installed"`},
		{"reinstalled", library.Reinstalled, `"reinstalled"`},
		{"install overwrite", library.InstallOptions{Overwrite: true}, `{"overwrite":true}`},
		{"install plan folder", library.InstallPlan{SkillID: 4, Name: "pdf", Folder: "/home/.claude/skills/pdf", Status: library.InstallOccupied, OccupierSkillID: 5}, `{"skill_id":4,"name":"pdf","folder":"/home/.claude/skills/pdf","status":"occupied","occupier_skill_id":5}`},
		{"group install plan members", library.GroupInstallPlan{Members: []library.InstallPlan{}}, `{"members":[]}`},
		{"skill scope folder", library.SkillScope{SkillID: 4, Scope: library.GlobalScope(library.AgentCodex), Folder: "/home/.agents/skills/pdf", Direct: true}, `{"skill_id":4,"scope":{"kind":"global","agent":"codex"},"folder":"/home/.agents/skills/pdf","direct":true}`},
		{"inspection version_id", library.SkillScopeInspection{SkillScope: library.SkillScope{SkillID: 4, Scope: library.GlobalScope(library.AgentCodex), Folder: "/f"}, Status: library.SkillScopeStale, VersionID: 3}, `{"skill_id":4,"scope":{"kind":"global","agent":"codex"},"folder":"/f","direct":false,"status":"stale","version_id":3}`},
		{"inspection root_missing", library.SkillScopeInspection{SkillScope: library.SkillScope{SkillID: 4, Scope: library.ProjectScope(library.AgentCodex, 2), Folder: "/f"}, Status: library.SkillScopeRootMissing}, `{"skill_id":4,"scope":{"kind":"project","agent":"codex","project_id":2},"folder":"/f","direct":false,"status":"root_missing"}`},
		{"reinstall request", library.ReinstallRequest{SkillID: 4, Scope: library.GlobalScope(library.AgentCodex), Overwrite: true}, `{"skill_id":4,"scope":{"kind":"global","agent":"codex"},"overwrite":true}`},
		{"skill scope filter", library.SkillScopeFilter{SkillID: 4}, `{"skill_id":4}`},
		{"preview fetch github", library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo@demo", Ref: "main", Subpath: "skills"}}, `{"github":{"input":"owner/repo@demo","ref":"main","subpath":"skills"}}`},
		{"preview fetch url", library.PreviewFetchRequest{URL: "https://example.com/skills.zip"}, `{"url":"https://example.com/skills.zip"}`},
		{"preview fetch local", library.PreviewFetchRequest{Local: "/skills"}, `{"local":"/skills"}`},
		{"preview fetch collection", library.PreviewFetchRequest{Collection: 2}, `{"collection":2}`},
		{"update check deleted", library.UpdateCheck{CheckedAt: at, Status: library.UpdateDeleted}, `{"checked_at":"2026-09-27T00:00:00Z","status":"deleted"}`},
		{"collection update check", library.CollectionUpdateCheck{CollectionID: 2, Source: library.Source{Type: library.SourceLocal, Label: "/skills"}, Skills: []library.Skill{}}, `{"collection_id":2,"source":{"type":"local","label":"/skills"}}`},
		{"collection update check not found", library.CollectionUpdateCheck{CollectionID: 9, Error: &library.Error{Code: library.NotFound, Message: "m"}}, `{"collection_id":9,"error":{"code":"not_found","message":"m"}}`},
		{"update check pending", library.UpdateCheck{CheckedAt: at, Status: library.UpdateAvailable, Revision: "abc", Skipped: false, Pending: true}, `{"checked_at":"2026-09-27T00:00:00Z","status":"available","revision":"abc","pending":true}`},
		{"update check skipped", library.UpdateCheck{CheckedAt: at, Status: library.UpdateAvailable, Revision: "abc", Skipped: true}, `{"checked_at":"2026-09-27T00:00:00Z","status":"available","revision":"abc","skipped":true}`},
		{"skill kept deleted", library.Skill{ID: 4, CollectionID: 2, VersionID: 3, KeptDeleted: true}, `{"id":4,"name":"","description":"","collection_id":2,"source":{"type":"","label":""},"version_id":3,"kept_deleted":true}`},
		{"candidate new", library.Candidate{New: true, Name: "demo"}, `{"new":true,"name":"demo","description":"","path":""}`},
		{"collection new skills", library.Collection{ID: 2, Source: library.Source{Type: library.SourceLocal, Label: "/skills"}, Skills: []library.SkillID{4}, NewSkills: []string{"xlsx"}}, `{"id":2,"source":{"type":"local","label":"/skills"},"skills":[4],"new_skills":["xlsx"]}`},
		{"update plan", library.UpdatePlan{ID: "p", CollectionID: 2, Source: library.Source{Type: library.SourceGitHub, Label: "owner/repo"}, Revision: "def", Commits: []library.Commit{{SHA: "def", Message: "fix", Author: "matt", Time: at}}, Skills: []library.PlannedSkill{{Skill: library.Skill{ID: 4, CollectionID: 2, VersionID: 3}, Change: library.PlannedChanged, Skipped: true, Risks: []library.UpdateRisk{{Kind: library.RiskScriptAdded, Path: "run.py"}}, Diff: &library.VersionDiff{Changed: []string{}, Added: []string{"run.py"}, Deleted: []string{}}}}, Installs: []library.SkillScopeInspection{}}, `{"id":"p","collection_id":2,"source":{"type":"github","label":"owner/repo"},"revision":"def","commits":[{"sha":"def","message":"fix","author":"matt","time":"2026-09-27T00:00:00Z"}],"skills":[{"skill":{"id":4,"name":"","description":"","collection_id":2,"source":{"type":"","label":""},"version_id":3},"change":"changed","skipped":true,"risks":[{"kind":"script_added","path":"run.py"}],"diff":{"changed":[],"added":["run.py"],"deleted":[]}}],"installs":[]}`},
		{"planned unchanged", library.PlannedSkill{Skill: library.Skill{ID: 4}, Change: library.PlannedUnchanged}, `{"skill":{"id":4,"name":"","description":"","collection_id":0,"source":{"type":"","label":""},"version_id":0},"change":"unchanged"}`},
		{"skip request", library.SkipRequest{Plan: "p", Skills: []library.SkillID{4}}, `{"plan":"p","skills":[4]}`},
		{"skip whole revision", library.SkipRequest{Plan: "p", WholeRevision: true}, `{"plan":"p","whole_revision":true}`},
		{"update request", library.UpdateRequest{Plan: "p", Skills: []library.SkillID{4, 5}, Installs: []library.ReinstallRequest{{SkillID: 4, Scope: library.GlobalScope(library.AgentCodex)}}}, `{"plan":"p","skills":[4,5],"installs":[{"skill_id":4,"scope":{"kind":"global","agent":"codex"},"overwrite":false}]}`},
		{"updated skill", library.UpdatedSkill{Skill: library.Skill{ID: 4, CollectionID: 2, VersionID: 8}, PreviousVersionID: 6, Changed: true}, `{"skill":{"id":4,"name":"","description":"","collection_id":2,"source":{"type":"","label":""},"version_id":8},"previous_version_id":6,"changed":true}`},
		{"update result", library.UpdateResult{Record: 3, Source: library.Source{Type: library.SourceLocal, Label: "/skills"}, Revision: "abc", Skills: []library.UpdatedSkill{}, Installs: []library.ReinstallResult{}}, `{"record":3,"source":{"type":"local","label":"/skills"},"revision":"abc","skills":[],"installs":[]}`},
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
