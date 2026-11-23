package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

func getSkillMentions(t *testing.T, lib *library.Library, id library.SkillID) library.SkillMentions {
	t.Helper()
	result, err := lib.GetSkillMentions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// mentionNames 把一栏写成名字的列表,便于比较。
func mentionNames(mentions []library.SkillMention) []string {
	names := []string{}
	for _, mention := range mentions {
		names = append(names, mention.Name)
	}
	return names
}

func install(t *testing.T, lib *library.Library, id library.SkillID, scope library.Scope) {
	t.Helper()
	if err := lib.Install(t.Context(), id, scope, library.InstallOptions{}); err != nil {
		t.Fatalf("Install(%d, %+v) = %v", id, scope, err)
	}
}

func listMissingMentions(t *testing.T, lib *library.Library) []library.MissingMention {
	t.Helper()
	missing, err := lib.ListMissingMentions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return missing
}

func listLostMentions(t *testing.T, lib *library.Library, gone ...library.SkillInScope) []library.MissingMention {
	t.Helper()
	lost, err := lib.ListLostMentions(t.Context(), gone)
	if err != nil {
		t.Fatal(err)
	}
	return lost
}

func requireMissingMentions(t *testing.T, got, want []library.MissingMention) {
	t.Helper()
	if want == nil {
		want = []library.MissingMention{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mentions =\n  %+v\nwant\n  %+v", got, want)
	}
}

// 技能详情的两栏:提到了谁、被谁提到,规则同关系图;被提到的一方也包括 Agent 目录里的未纳管技能(SkillID 为 0),
// 不在库里、也不在任何 Agent 目录里的名字认不出。单个词的名字没判断时只计入 Unjudged,两个方向都算。
func TestGetSkillMentionsListsBothDirectionsAndUnmanagedNames(t *testing.T) {
	t.Parallel()
	root := mentionSource(t, map[string]string{
		"entry":         "Run /ship, then writing-plans.\nAlso try /local-tool and /nowhere.\nCould you review this?\n",
		"ship":          "# ship\n",
		"writing-plans": "# writing-plans\n",
		"review":        "# review\n",
		"helper":        "Start with /entry.\n",
		"quiet":         "Ask entry for help.\n",
	})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)
	writeUnmanagedSkill(t, scopeDirectory(t, lib, library.GlobalScope(library.AgentClaudeCode)), "local-tool", "A tool written by hand")

	got := getSkillMentions(t, lib, skills["entry"].ID)
	want := library.SkillMentions{
		Mentions: []library.SkillMention{
			{Name: "local-tool", Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Also try /local-tool and /nowhere."}}},
			{Name: "ship", SkillID: skills["ship"].ID, Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Run /ship, then writing-plans."}}},
			{Name: "writing-plans", SkillID: skills["writing-plans"].ID, Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Run /ship, then writing-plans."}}},
		},
		MentionedBy: []library.SkillMention{
			{Name: "helper", SkillID: skills["helper"].ID, Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Start with /entry."}}},
		},
		Unjudged: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetSkillMentions(entry) =\n  %+v\nwant\n  %+v", got, want)
	}

	got = getSkillMentions(t, lib, skills["review"].ID)
	if len(got.Mentions) != 0 || len(got.MentionedBy) != 0 || got.Unjudged != 1 {
		t.Fatalf("GetSkillMentions(review) = %+v; want only one unjudged place (entry's review)", got)
	}
	_, err := lib.GetSkillMentions(t.Context(), 9999)
	requireLibraryError(t, err, library.NotFound)
}

// 详情里的判断只判断和这个技能有关的几处:它正文里单个词的名字,以及别的技能正文里它的名字;判断完两栏补上,
// 再判断不再请求。别的技能里和它无关的单个词名字不判断。
func TestJudgeSkillMentionsJudgesBothDirectionsOfOneSkill(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeJudgeSkill(t, filepath.Join(root, "entry"), "Entry", "Use the review skill here.\nAsk spec for nothing.\n")
	writeJudgeSkill(t, filepath.Join(root, "other"), "Other", "Hand it to review skill.\nWrite a spec skill.\n")
	writeJudgeSkill(t, filepath.Join(root, "review"), "Reviews a pull request", "Then ask entry.\n")
	writeJudgeSkill(t, filepath.Join(root, "spec"), "Writes a spec", "# spec\n")
	lib, stub := judgingLibrary(t)
	skills := fetchMentionSource(t, lib, root)

	if got := getSkillMentions(t, lib, skills["review"].ID).Unjudged; got != 3 {
		t.Fatalf("Unjudged(review) = %d, want 3 (its own entry, entry's review, other's review)", got)
	}
	result, err := lib.JudgeSkillMentions(t.Context(), skills["review"].ID)
	if err != nil || result.Judged != 3 || result.Failed != nil {
		t.Fatalf("JudgeSkillMentions = %+v, %v; want 3 judged", result, err)
	}
	if got, want := judgedNames(t, stub.takeCalls()), []string{"entry: Then ask entry.", "review: Hand it to review skill.", "review: Use the review skill here."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %q; want %q", got, want)
	}
	got := getSkillMentions(t, lib, skills["review"].ID)
	if names := mentionNames(got.MentionedBy); !reflect.DeepEqual(names, []string{"entry", "other"}) || got.Unjudged != 0 {
		t.Fatalf("after judging, MentionedBy = %q, Unjudged = %d; want entry and other, 0", names, got.Unjudged)
	}
	if names := mentionNames(got.Mentions); len(names) != 0 {
		t.Fatalf("after judging, review mentions %q; want none (the line has no word skill)", names)
	}
	if result, err := lib.JudgeSkillMentions(t.Context(), skills["review"].ID); err != nil || result.Judged != 0 || len(stub.takeCalls()) != 0 {
		t.Fatalf("judging again = %+v, %v; want no requests", result, err)
	}
	if got := getSkillMentions(t, lib, skills["spec"].ID).Unjudged; got != 2 {
		t.Fatalf("Unjudged(spec) = %d, want 2: judging review leaves spec alone", got)
	}
	_, err = lib.JudgeSkillMentions(t.Context(), 9999)
	requireLibraryError(t, err, library.NotFound)
}

// 装在作用域里的技能提到的技能,在那里找不到的列出来:按磁盘上的文件夹名找,库装的和未纳管技能都算;
// 项目作用域还看同一个 Agent 的全局目录,全局作用域不看项目。库里有的给出库里的技能,能装过去;
// 库里没有、只在别的 Agent 目录里的未纳管技能没有库里的技能。
func TestListMissingMentionsLooksInTheScopeAndTheAgentsGlobalDirectory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := mentionSource(t, map[string]string{
		"entry":  "Run /ship, /helper and /local-tool.\n",
		"ship":   "# ship\n",
		"helper": "# helper\n",
	})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)
	global := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	install(t, lib, skills["entry"].ID, global)
	writeUnmanagedSkill(t, scopeDirectory(t, lib, codex), "local-tool", "Written by hand for Codex")

	entry, ship, helper := skills["entry"].ID, skills["ship"].ID, skills["helper"].ID
	requireMissingMentions(t, listMissingMentions(t, lib), []library.MissingMention{
		{SkillID: entry, Scope: global, Name: "helper", MentionedSkillID: helper},
		{SkillID: entry, Scope: global, Name: "local-tool"},
		{SkillID: entry, Scope: global, Name: "ship", MentionedSkillID: ship},
	})

	writeUnmanagedSkill(t, scopeDirectory(t, lib, global), "local-tool", "Written by hand")
	install(t, lib, ship, global)
	requireMissingMentions(t, listMissingMentions(t, lib), []library.MissingMention{
		{SkillID: entry, Scope: global, Name: "helper", MentionedSkillID: helper},
	})

	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inProject := library.ProjectScope(library.AgentClaudeCode, project.ID)
	install(t, lib, entry, inProject)
	install(t, lib, helper, inProject)
	install(t, lib, entry, codex)
	requireMissingMentions(t, listMissingMentions(t, lib), []library.MissingMention{
		{SkillID: entry, Scope: global, Name: "helper", MentionedSkillID: helper},
		{SkillID: entry, Scope: codex, Name: "helper", MentionedSkillID: helper},
		{SkillID: entry, Scope: codex, Name: "ship", MentionedSkillID: ship},
	})
}

// 卸载、从库里删除之前看会有哪些技能因此找不到它:当那几处的文件夹已经不在,靠全局目录找到它的项目里的技能也算;
// 卸掉的技能自己不再列出;卸掉之前就已经找不到的不算。作用域不合法返回 InvalidArgument。
func TestListLostMentionsCountsOnlyWhatTheRemovalBreaks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := mentionSource(t, map[string]string{"entry": "Run /ship and /helper.\n", "ship": "# ship\n", "helper": "# helper\n"})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentClaudeCode)
	inProject := library.ProjectScope(library.AgentClaudeCode, project.ID)
	entry, ship := skills["entry"].ID, skills["ship"].ID
	install(t, lib, ship, global)
	install(t, lib, entry, global)
	install(t, lib, entry, inProject)
	if got := listMissingMentions(t, lib); len(got) != 2 {
		t.Fatalf("ListMissingMentions = %+v; want helper missing in both places", got)
	}

	requireMissingMentions(t, listLostMentions(t, lib, library.SkillInScope{SkillID: ship, Scope: global}), []library.MissingMention{
		{SkillID: entry, Scope: global, Name: "ship", MentionedSkillID: ship},
		{SkillID: entry, Scope: inProject, Name: "ship", MentionedSkillID: ship},
	})
	requireMissingMentions(t, listLostMentions(t, lib,
		library.SkillInScope{SkillID: ship, Scope: global}, library.SkillInScope{SkillID: entry, Scope: global},
	), []library.MissingMention{
		{SkillID: entry, Scope: inProject, Name: "ship", MentionedSkillID: ship},
	})
	requireMissingMentions(t, listLostMentions(t, lib, library.SkillInScope{SkillID: ship, Scope: inProject}), nil)
	requireMissingMentions(t, listLostMentions(t, lib), nil)

	_, err = lib.ListLostMentions(ctx, []library.SkillInScope{{SkillID: ship, Scope: library.Scope{Kind: "nowhere", Agent: library.AgentClaudeCode}}})
	requireLibraryError(t, err, library.InvalidArgument)
}

// Cursor 和 Codex 共用 ~/.agents/skills,ship 装在这里,Cursor 自己的目录里还有一个指回它的旧链接:卸载时旧链接随真实文件夹一并删掉,
// 所以 Cursor 里提到 ship 的技能也会找不到它,和 Codex 的一起列出。
func TestListLostMentionsCountsStaleLinksAsGone(t *testing.T) {
	t.Parallel()
	lib, agents, cursor := cursorHome(t)
	skills := fetchMentionSource(t, lib, mentionSource(t, map[string]string{"entry": "Run /ship.\n", "ship": "# ship\n"}))
	codex := library.GlobalScope(library.AgentCodex)
	entry, ship := skills["entry"].ID, skills["ship"].ID
	install(t, lib, ship, codex)
	install(t, lib, entry, codex)
	if err := os.MkdirAll(cursor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(agents, "ship"), filepath.Join(cursor, "ship")); err != nil {
		t.Fatal(err)
	}
	requireMissingMentions(t, listLostMentions(t, lib, library.SkillInScope{SkillID: ship, Scope: codex}), []library.MissingMention{
		{SkillID: entry, Scope: codex, Name: "ship", MentionedSkillID: ship},
		{SkillID: entry, Scope: library.GlobalScope("cursor"), Name: "ship", MentionedSkillID: ship},
	})
}

// 装着的技能自己的文件夹被删掉了时,它已经不在那里,不再提示它找不到别的技能。
func TestListMissingMentionsSkipsSkillsWhoseFolderIsGone(t *testing.T) {
	t.Parallel()
	root := mentionSource(t, map[string]string{"entry": "Run /ship.\n", "ship": "# ship\n"})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)
	global := library.GlobalScope(library.AgentClaudeCode)
	install(t, lib, skills["entry"].ID, global)
	if got := listMissingMentions(t, lib); len(got) != 1 {
		t.Fatalf("ListMissingMentions = %+v; want entry missing ship", got)
	}
	if err := os.RemoveAll(filepath.Join(scopeDirectory(t, lib, global), "entry")); err != nil {
		t.Fatal(err)
	}
	requireMissingMentions(t, listMissingMentions(t, lib), nil)
}
