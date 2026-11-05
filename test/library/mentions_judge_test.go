package library_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// judgeItem 是判断请求里的一处:被提到的技能的名字、描述,和出现这个名字的几行。
type judgeItem struct {
	Name, Description string
	Lines             []string
}

var judgeItemPattern = regexp.MustCompile(`(?m)^Item (\d+)\nSkill name: (.*)\nSkill description: (.*)\nLines:\n((?:> .*\n?)*)`)

// judgeItems 从判断请求的用户消息里读出每一处。
func judgeItems(t *testing.T, user string) []judgeItem {
	t.Helper()
	var items []judgeItem
	for _, match := range judgeItemPattern.FindAllStringSubmatch(user, -1) {
		item := judgeItem{Name: match[2], Description: match[3]}
		for line := range strings.Lines(match[4]) {
			item.Lines = append(item.Lines, strings.TrimSuffix(strings.TrimPrefix(line, "> "), "\n"))
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		t.Errorf("no items in the judge request %q", user)
	}
	return items
}

// answerBySkillWord 是假 AI 服务的判断:几行里写了 skill 这个词的那一处算提到,回答这些编号。
func answerBySkillWord(body map[string]any) string {
	_, user := userText(aiCall{body: body})
	var yes []string
	for _, match := range judgeItemPattern.FindAllStringSubmatch(user, -1) {
		if strings.Contains(match[4], "skill") {
			yes = append(yes, match[1])
		}
	}
	return "[" + strings.Join(yes, ", ") + "]"
}

// writeJudgeSkill 写一个带描述的技能文件夹。
func writeJudgeSkill(t *testing.T, dir, description, body string) {
	t.Helper()
	document := "---\nname: " + filepath.Base(dir) + "\ndescription: " + description + "\n---\n" + body
	writeFile(t, filepath.Join(dir, "SKILL.md"), []byte(document), 0o644)
}

func judgingLibrary(t *testing.T) (*library.Library, *aiStub) {
	t.Helper()
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	stub.replyWith(answerBySkillWord)
	return lib, stub
}

func judgeMentions(t *testing.T, lib *library.Library, filter library.MentionsFilter) library.JudgeMentionsResult {
	t.Helper()
	result, err := lib.JudgeMentions(t.Context(), filter)
	if err != nil {
		t.Fatalf("JudgeMentions = %+v, %v", result, err)
	}
	return result
}

func unjudged(t *testing.T, lib *library.Library, filter library.MentionsFilter) int {
	t.Helper()
	result, err := lib.ListMentions(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	return result.Unjudged
}

// judgedNames 把每个判断请求写成「名字: 第一行」,几处用 | 隔开,便于看每个请求判断的是谁的哪几处。
func judgedNames(t *testing.T, calls []aiCall) []string {
	t.Helper()
	var names []string
	for _, call := range calls {
		var request []string
		for _, item := range judgeItems(t, userTextOf(call)) {
			request = append(request, item.Name+": "+item.Lines[0])
		}
		names = append(names, strings.Join(request, " | "))
	}
	slices.Sort(names)
	return names
}

func userTextOf(call aiCall) string {
	_, user := userText(call)
	return user
}

// 单个词的技能名没用点名写法时才交给 AI:点名写法、几个词连成的名字靠规则就画线;已经靠规则连上的名字、
// 路径中间的一段、网址、文件名和公共段落里的出现都不交。每个技能一个请求,一个名字一处,每处只带出现这个名字的
// 前 3 行(不同的行)和被提到技能的名字、描述,不带这个技能自己的名字、描述和别的行。判断为提到的画线,
// 悬停原文就是交给 AI 的那几行;判断为不是的不画。
func TestJudgeMentionsAsksOnlySingleWordNamesWithoutInvocationForms(t *testing.T) {
	shared := "Shared: ask review for help."
	root := t.TempDir()
	writeJudgeSkill(t, filepath.Join(root, "entry"), "Entry point of the set", strings.Join([]string{
		"Run /ship when done, then writing-plans.",
		"Could you review this?",
		"Use the review skill for the diff.",
		"Could you review this?",
		"review once more",
		"A fourth review line.",
		"Run /spec first, then treat it as a spec.",
		"See docs/pdf/guide.md, https://example.com/pdf and pdf.md.",
		"Ship it.",
		shared,
	}, "\n")+"\n")
	writeJudgeSkill(t, filepath.Join(root, "helper"), "Helps", "Ask for a review when stuck.\n"+shared+"\n")
	writeJudgeSkill(t, filepath.Join(root, "quiet"), "Quiet", "# quiet\n"+shared+"\n")
	writeJudgeSkill(t, filepath.Join(root, "ship"), "Ships the change", "# ship\n"+shared+"\n")
	writeJudgeSkill(t, filepath.Join(root, "pdf"), "Reads PDF files", "# pdf\n"+shared+"\n")
	writeJudgeSkill(t, filepath.Join(root, "spec"), "Writes a spec", "# spec\n")
	writeJudgeSkill(t, filepath.Join(root, "writing-plans"), "Writes plans", "# writing-plans\n")
	writeJudgeSkill(t, filepath.Join(root, "review"), "Reviews a pull request", "# review\n")
	lib, stub := judgingLibrary(t)
	skills := fetchMentionSource(t, lib, root)
	filter := library.MentionsFilter{CollectionID: skills["entry"].CollectionID}

	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> ship", "entry -> spec", "entry -> writing-plans"})
	if n := unjudged(t, lib, filter); n != 2 {
		t.Fatalf("Unjudged = %d, want 2 (review in entry and in helper)", n)
	}
	if got := judgeMentions(t, lib, filter); got.Judged != 2 || got.Failed != nil {
		t.Fatalf("JudgeMentions = %+v; want 2 judged", got)
	}
	calls := stub.takeCalls()
	if len(calls) != 2 {
		t.Fatalf("%d requests, want one for entry and one for helper", len(calls))
	}
	var entryRequest []judgeItem
	for _, call := range calls {
		system, user := userText(call)
		if strings.Contains(system, "entry") || strings.Contains(system, "Entry point") {
			t.Errorf("system prompt %q names the skill being read", system)
		}
		for _, leaked := range []string{"entry", "Entry point", "helper", "Helps", "/ship", "spec", "pdf", "Shared", "fourth"} {
			if strings.Contains(user, leaked) {
				t.Errorf("request %q carries %q; want only the lines and the mentioned skill", user, leaked)
			}
		}
		if items := judgeItems(t, user); strings.Contains(user, "Use the review skill") {
			entryRequest = items
		}
	}
	want := []judgeItem{{Name: "review", Description: "Reviews a pull request", Lines: []string{"Could you review this?", "Use the review skill for the diff.", "review once more"}}}
	if !reflect.DeepEqual(entryRequest, want) {
		t.Fatalf("entry's request = %#v; want %#v", entryRequest, want)
	}

	mentions, err := listMentions(t, lib, filter)
	if err != nil {
		t.Fatal(err)
	}
	var lines []library.MentionLine
	for _, mention := range mentions {
		if mention.From == skills["entry"].ID && mention.To == skills["review"].ID {
			lines = mention.Lines
		}
	}
	wantLines := []library.MentionLine{
		{Path: "SKILL.md", Text: "Could you review this?"},
		{Path: "SKILL.md", Text: "Use the review skill for the diff."},
		{Path: "SKILL.md", Text: "review once more"},
	}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("entry -> review lines = %#v; want %#v", lines, wantLines)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> review", "entry -> ship", "entry -> spec", "entry -> writing-plans"})
	if n := unjudged(t, lib, filter); n != 0 {
		t.Fatalf("Unjudged after judging = %d, want 0", n)
	}
}

// 结论随技能的版本存下:同一个版本只判断一次,再判断、重开数据库都不再请求;只判断这组技能,组外的不判断。
// 技能出了新版本只重判它自己;新入库了单个词名字的技能,只判断正文里出现这个名字的那一处。
func TestJudgeMentionsJudgesEachVersionOnce(t *testing.T) {
	stub := newAIStub(t)
	stub.replyWith(answerBySkillWord)
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openTestLibraryAt(t, path)
	setAISettings(t, lib, library.AISettings{BaseURL: stub.url + "/v1", APIKey: "sk-test", Model: "model-test"})
	entry := fetchSkillWithBody(t, lib, "entry", "Use the review skill.\n")
	other, otherSource := fetchLocalSkill(t, lib, "other", "Please review the output.\n")
	fetchSkillWithBody(t, lib, "outsider", "Use the review skill too.\n")
	fetchSkillWithBody(t, lib, "review", "# review\n")
	group, err := lib.CreateGroup(t.Context(), "pair")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{entry, other} {
		if _, err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	filter := library.MentionsFilter{GroupID: group.ID}

	if got := judgeMentions(t, lib, filter); got.Judged != 2 {
		t.Fatalf("JudgeMentions = %+v; want 2 judged", got)
	}
	if got := judgedNames(t, stub.takeCalls()); !reflect.DeepEqual(got, []string{"review: Please review the output.", "review: Use the review skill."}) {
		t.Fatalf("requests = %q; want entry and other, not the outsider", got)
	}
	if got := judgeMentions(t, lib, filter); got.Judged != 0 {
		t.Fatalf("JudgeMentions again = %+v; want nothing left", got)
	}
	if n := len(stub.takeCalls()); n != 0 {
		t.Fatalf("%d requests on the second JudgeMentions, want none", n)
	}

	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib = openTestLibraryAt(t, path)
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> review"})
	if n := unjudged(t, lib, filter); n != 0 {
		t.Fatalf("Unjudged after reopening = %d, want 0", n)
	}
	judgeMentions(t, lib, filter)
	if n := len(stub.takeCalls()); n != 0 {
		t.Fatalf("%d requests after reopening, want none", n)
	}

	writeLocalSkill(t, otherSource, "Use the review skill, then lint.\n")
	if _, err := updateSkill(t, lib, t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	if n := unjudged(t, lib, filter); n != 1 {
		t.Fatalf("Unjudged after other's new version = %d, want 1", n)
	}
	judgeMentions(t, lib, filter)
	if got := judgedNames(t, stub.takeCalls()); !reflect.DeepEqual(got, []string{"review: Use the review skill, then lint."}) {
		t.Fatalf("requests after other's new version = %q; want only other", got)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> review", "other -> review"})

	fetchSkillWithBody(t, lib, "lint", "# lint\n")
	if n := unjudged(t, lib, filter); n != 1 {
		t.Fatalf("Unjudged after fetching lint = %d, want 1", n)
	}
	judgeMentions(t, lib, filter)
	if got := judgedNames(t, stub.takeCalls()); !reflect.DeepEqual(got, []string{"lint: Use the review skill, then lint."}) {
		t.Fatalf("requests after fetching lint = %q; want only lint in other", got)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> review", "other -> lint", "other -> review"})
}

// 只因某一个技能没判断成的(服务拒绝了这一条、回答被截断、回答不是编号的数组)记进 Failed,它的几处还算没判断,
// 其余照常判断、存下;整体性的失败(如 Key 被拒)返回错误,已判断的保留。
func TestJudgeMentionsSkipsSkillsThatFailAndStopsOnOverallFailures(t *testing.T) {
	lib, stub := judgingLibrary(t)
	group, err := lib.CreateGroup(t.Context(), "all")
	if err != nil {
		t.Fatal(err)
	}
	var blocked library.SkillID
	for _, name := range []string{"alpha", "bravo", "charlie", "delta"} {
		body := "Use the review skill in " + name + ".\n"
		if name == "charlie" {
			body = "Something the filter blocks: review.\n"
		}
		skill := fetchSkillWithBody(t, lib, name, body)
		if name == "charlie" {
			blocked = skill.ID
		}
		if _, err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	fetchSkillWithBody(t, lib, "review", "# review\n")
	filter := library.MentionsFilter{GroupID: group.ID}

	stub.rejectContaining("the filter blocks")
	got, err := lib.JudgeMentions(t.Context(), filter)
	if err != nil || got.Judged != 3 || len(got.Failed) != 1 || got.Failed[0].SkillID != blocked || got.Failed[0].Error.Code != library.InvalidArgument {
		t.Fatalf("JudgeMentions = %+v, %v; want 3 judged and charlie failed with InvalidArgument", got, err)
	}
	if n := unjudged(t, lib, filter); n != 1 {
		t.Fatalf("Unjudged = %d, want charlie's one left", n)
	}
	stub.takeCalls()

	stub.rejectContaining("")
	stub.replyWith(func(map[string]any) string { return "Item 1 is about the skill." })
	if got, err := lib.JudgeMentions(t.Context(), filter); err != nil || len(got.Failed) != 1 || got.Failed[0].Error.Code != library.InvalidArgument {
		t.Fatalf("JudgeMentions with an answer that is not an array = %+v, %v; want charlie failed with InvalidArgument", got, err)
	}
	stub.replyWith(func(map[string]any) string { return "[1, 2]" })
	if got, err := lib.JudgeMentions(t.Context(), filter); err != nil || len(got.Failed) != 1 {
		t.Fatalf("JudgeMentions with an item that was not asked = %+v, %v; want charlie failed", got, err)
	}
	stub.replyWith(answerBySkillWord)
	stub.truncate(true)
	if got, err := lib.JudgeMentions(t.Context(), filter); err != nil || len(got.Failed) != 1 || got.Failed[0].Error.Details.Field != "max_tokens" {
		t.Fatalf("JudgeMentions cut off = %+v, %v; want charlie failed on max_tokens", got, err)
	}
	stub.truncate(false)

	stub.respond(401)
	if got, err := lib.JudgeMentions(t.Context(), filter); !library.IsCode(err, library.Permission) || got.Judged != 0 || got.Failed != nil {
		t.Fatalf("JudgeMentions with the key rejected = %+v, %v; want Permission", got, err)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"alpha -> review", "bravo -> review", "delta -> review"})
	if n := unjudged(t, lib, filter); n != 1 {
		t.Fatalf("Unjudged after the overall failure = %d, want 1", n)
	}
}

// 没填模型时有要判断的就返回 NotConfigured,不发请求,规则确定的线照画;没有要判断的不看 AI 设置。
// 参数和集合、组的错误同 ListMentions。
func TestJudgeMentionsErrors(t *testing.T) {
	stub := newAIStub(t)
	lib := openTestLibrary(t)
	entry := fetchSkillWithBody(t, lib, "entry", "Use the review skill, then /ship.\n")
	fetchSkillWithBody(t, lib, "review", "# review\n")
	ship := fetchSkillWithBody(t, lib, "ship", "# ship\n")
	filter := library.MentionsFilter{CollectionID: entry.CollectionID}

	if _, err := lib.JudgeMentions(t.Context(), filter); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("JudgeMentions without a model = %v, want NotConfigured", err)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> ship"})
	if n := unjudged(t, lib, filter); n != 1 {
		t.Fatalf("Unjudged without a model = %d, want 1", n)
	}
	if got, err := lib.JudgeMentions(t.Context(), library.MentionsFilter{CollectionID: ship.CollectionID}); err != nil || got.Judged != 0 {
		t.Fatalf("JudgeMentions with nothing to judge = %+v, %v; want nothing and no error", got, err)
	}

	setAISettings(t, lib, library.AISettings{BaseURL: stub.url, APIKey: "sk-test", Model: "gpt-test"})
	for _, bad := range []library.MentionsFilter{{}, {CollectionID: entry.CollectionID, GroupID: 1}, {GroupID: -1}} {
		_, err := lib.JudgeMentions(t.Context(), bad)
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "collection_id"})
	}
	for _, missing := range []library.MentionsFilter{{CollectionID: entry.CollectionID + 100}, {GroupID: 100}} {
		_, err := lib.JudgeMentions(t.Context(), missing)
		requireLibraryError(t, err, library.NotFound)
	}
	if got := stub.take(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none", got)
	}
}

// 判断结论随技能一并删除:移除判断过的技能不受影响,被提到的技能移除后线也不再画。
func TestRemoveSkillWithMentionJudgments(t *testing.T) {
	lib, _ := judgingLibrary(t)
	entry := fetchSkillWithBody(t, lib, "entry", "Use the review skill.\n")
	review := fetchSkillWithBody(t, lib, "review", "# review\n")
	filter := library.MentionsFilter{CollectionID: entry.CollectionID}
	judgeMentions(t, lib, filter)
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> review"})
	if err := lib.RemoveSkill(t.Context(), review.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{})
	if err := lib.RemoveSkill(t.Context(), entry.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatalf("RemoveSkill of a judged skill = %v", err)
	}
}

// 打开旧版本(最早支持升级的 schema 版本)建的数据库时升级到存判断结果的新结构:原来的技能都在,
// 之后照常判断、存下结论。
func TestJudgeMentionsAfterUpgradingAnOldDatabase(t *testing.T) {
	fixtures := filepath.Join("..", "..", "internal", "library", "internal", "store", "testdata")
	schemas, err := filepath.Glob(filepath.Join(fixtures, "*_schema.sql"))
	if err != nil || len(schemas) != 1 {
		t.Fatalf("baseline schemas = %v, %v; want exactly one", schemas, err)
	}
	version, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(schemas[0]), "_schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(schemas[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(fixtures, fmt.Sprintf("%05d_data.sql", version)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "library.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(string(schema) + string(data) + fmt.Sprintf("PRAGMA application_id = 1397443650; PRAGMA user_version = %d;", version)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	stub := newAIStub(t)
	stub.replyWith(answerBySkillWord)
	lib := openTestLibraryAt(t, path)
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), fmt.Sprintf("library-v%d.db", version))); err != nil {
		t.Fatalf("backup of the old database: %v", err)
	}
	old, err := lib.ListSkills(t.Context())
	if err != nil || len(old) == 0 {
		t.Fatalf("ListSkills after upgrading = %v, %v; want the old skills", old, err)
	}
	setAISettings(t, lib, library.AISettings{BaseURL: stub.url + "/v1", APIKey: "sk-test", Model: "model-test"})
	entry := fetchSkillWithBody(t, lib, "entry", "Use the reviewer skill.\n")
	fetchSkillWithBody(t, lib, "reviewer", "# reviewer\n")
	filter := library.MentionsFilter{CollectionID: entry.CollectionID}
	if got := judgeMentions(t, lib, filter); got.Judged != 1 {
		t.Fatalf("JudgeMentions after upgrading = %+v; want 1 judged", got)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> reviewer"})
	if n := unjudged(t, lib, filter); n != 0 {
		t.Fatalf("Unjudged = %d, want 0", n)
	}
}
