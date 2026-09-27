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
		{"skill summary fetched_at", library.SkillSummary{FetchedAt: at}, `{"id":0,"name":"","description":"","source":{"id":0,"type":"","locator":{},"last_fetched":"0001-01-01T00:00:00Z","last_seen_ref":""},"fetched_at":"2026-09-27T00:00:00Z"}`},
		{"group install created_at", library.GroupInstall{GroupID: 1, Scope: library.GlobalScope(library.AgentCodex), CreatedAt: at}, `{"group_id":1,"scope":{"kind":"global","agent":"codex"},"created_at":"2026-09-27T00:00:00Z"}`},
		{"remove skill installations", library.RemoveSkillOptions{Installations: library.Keep}, `{"installations":"keep"}`},
		{"unregister project installations", library.UnregisterProjectOptions{Installations: library.Remove}, `{"installations":"remove"}`},
		{"remove from group unbacked_installations", library.RemoveFromGroupOptions{UnbackedInstallations: library.WithdrawInstallation}, `{"unbacked_installations":"withdraw"}`},
		{"install plan already installed", library.InstallAlreadyInstalled, `"already_installed"`},
		{"member installed", library.MemberInstalled, `"installed"`},
		{"member already installed", library.MemberAlreadyInstalled, `"already_installed"`},
		{"reinstalled", library.Reinstalled, `"reinstalled"`},
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
