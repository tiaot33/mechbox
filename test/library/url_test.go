package library_test

import (
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func fetchArchive(t *testing.T, lib *library.Library, spec library.PreviewFetchRequest) []library.Skill {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return fetchSkills(t, lib, preview, candidateNames(preview))
}

// 服务端给出各种写法的 validator 并回 304 时,检查与更新都判定无更新,只刷新读取时间。
func TestURLNotModifiedMeansNoUpdate(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	for _, tc := range []struct{ name, etag, lastModified string }{
		{"strong etag", `"v1"`, ""},
		{"weak etag", `W/"weak-v1"`, ""},
		{"unquoted etag", `unquoted-v1`, ""},
		{"last modified", "", "Tue, 15 Sep 2026 10:00:00 GMT"},
		{"malformed last modified", "", "yesterday-ish"},
		{"no validator", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newArchiveServer(t, archive, tc.etag)
			if tc.lastModified != "" {
				server.serveLastModified(archive, tc.lastModified)
			}
			lib := openTestLibrary(t)
			id := fetchArchive(t, lib, server.request())[0].ID
			before, err := lib.GetSkill(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateCurrent {
				t.Fatalf("CheckUpdates = %v, %v", status, err)
			}
			result, err := updateSkill(t, lib, t.Context(), id)
			if err != nil || result.Changed {
				t.Fatalf("Update = %#v, %v", result, err)
			}
			if result.Skill.Revision == "" || result.Skill.Revision != before.Revision || !result.Skill.UpdatedAt.IsZero() {
				t.Fatalf("skill after no-op update = %#v, before %#v", result.Skill, before)
			}
			if versions, err := lib.ListVersions(t.Context(), id); err != nil || len(versions) != 1 {
				t.Fatalf("versions = %#v, %v", versions, err)
			}
		})
	}
}

func TestURLUpdateWritesChangedArchive(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")}), `"v1"`)
	lib := openTestLibrary(t)
	id := fetchArchive(t, lib, server.request())[0].ID

	server.serve(zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")}), `"v2"`)
	if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateAvailable {
		t.Fatalf("CheckUpdates after remote change = %v, %v", status, err)
	}
	result, err := updateSkill(t, lib, t.Context(), id)
	if err != nil || !result.Changed || string(skillFiles(t, lib, result.Skill)[0].Content) != skillDocument("demo", "new") {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	if versions, err := lib.ListVersions(t.Context(), id); err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	if status, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || status != library.UpdateCurrent {
		t.Fatalf("CheckUpdates after Update = %v, %v", status, err)
	}
}

// 同一压缩包里的技能各自判断:只更新了其中一个后,另一个仍报告有更新。
func TestURLSkillsFromOneArchiveAreCheckedIndependently(t *testing.T) {
	server := newArchiveServer(t, zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "old"),
	}), `"v1"`)
	lib := openTestLibrary(t)
	ids := skillIDs(fetchArchive(t, lib, server.request()))
	requireAvailable := func(want ...bool) {
		t.Helper()
		for i, id := range ids {
			status, err := checkSkillUpdate(t, lib, t.Context(), id)
			if err != nil || (status == library.UpdateAvailable) != want[i] || (status != library.UpdateAvailable && status != library.UpdateCurrent) {
				t.Fatalf("CheckUpdates(%d) = %v, %v; want available %v", id, status, err, want[i])
			}
		}
	}
	requireAvailable(false, false)

	server.serve(zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "new"),
	}), `"v2"`)
	requireAvailable(false, true)

	server.serve(zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "new"),
		"second/SKILL.md": skillDocument("second", "new"),
	}), `"v3"`)
	if result, err := updateSkill(t, lib, t.Context(), ids[0]); err != nil || !result.Changed {
		t.Fatalf("Update first = %#v, %v", result, err)
	}
	requireAvailable(false, true)
}

// 没带条件头却收到 304 是远端异常,不能当成"无更新"。
func TestURLUnexpectedNotModifiedIsAnError(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := newArchiveServer(t, archive, "")
	lib := openTestLibrary(t)
	id := fetchArchive(t, lib, server.request())[0].ID
	server.serveNotModified()
	if preview, err := lib.PreviewFetch(t.Context(), server.request()); !library.IsCode(err, library.SourceUnreachable) || preview.ID != "" {
		t.Fatalf("PreviewFetch = %#v, %v, want SourceUnreachable", preview, err)
	}
	if status, err := checkSkillUpdate(t, lib, t.Context(), id); !library.IsCode(err, library.SourceUnreachable) || status != library.UpdateFailed {
		t.Fatalf("CheckUpdates = %v, %v, want SourceUnreachable", status, err)
	}
}
