package library_test

import (
	"path/filepath"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

func noteOf(t *testing.T, lib *library.Library, id library.SkillID) string {
	t.Helper()
	skill, err := lib.GetSkill(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return skill.Note
}

// 备注去掉首尾空白存进数据库,读技能和列出技能都带着它,重新打开仍在;更新技能不动备注,留空即删掉。
func TestSkillNoteIsKeptAndCleared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	first := openTestLibraryAt(t, path)
	skill, source := fetchLocalSkill(t, first, "formatter", "# formatter\n")
	if skill.Note != "" {
		t.Fatalf("note of a fetched skill = %q, want empty", skill.Note)
	}
	if err := first.SetSkillNote(t.Context(), skill.ID, "  用在前端项目\n别删\n "); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	lib := openTestLibraryAt(t, path)
	if got := noteOf(t, lib, skill.ID); got != "用在前端项目\n别删" {
		t.Fatalf("note = %q", got)
	}
	writeLocalSkill(t, source, "# formatter v2\n")
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	skills, err := lib.ListSkills(t.Context())
	if err != nil || len(skills) != 1 || skills[0].Note != "用在前端项目\n别删" {
		t.Fatalf("ListSkills after an update = %+v, %v; want the note kept", skills, err)
	}
	if err := lib.SetSkillNote(t.Context(), skill.ID, " \n "); err != nil {
		t.Fatal(err)
	}
	if got := noteOf(t, lib, skill.ID); got != "" {
		t.Fatalf("note after clearing = %q", got)
	}
}

// 给不存在的技能写备注返回 NotFound。
func TestSkillNoteOfMissingSkill(t *testing.T) {
	lib := openTestLibrary(t)
	if err := lib.SetSkillNote(t.Context(), 9999, "note"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("SetSkillNote = %v, want NotFound", err)
	}
}
