package library_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// mentionSource 在临时目录里写一个本地来源:键是技能名,值是 SKILL.md 正文;返回来源目录。
func mentionSource(t *testing.T, skills map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range skills {
		writeLocalSkill(t, filepath.Join(root, name), body)
	}
	return root
}

// fetchMentionSource 把来源目录里的技能全部入库,返回按名字找技能的表。
func fetchMentionSource(t *testing.T, lib *library.Library, root string) map[string]library.Skill {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]library.Skill{}
	for _, skill := range fetchSkills(t, lib, preview, candidateNames(preview)) {
		byName[skill.Name] = skill
	}
	return byName
}

// mentionPairs 把关系写成「提到的 -> 被提到的」,按字母排序,便于和期望比较。
func mentionPairs(t *testing.T, lib *library.Library, filter library.MentionsFilter) []string {
	t.Helper()
	mentions, err := lib.ListMentions(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	label := map[library.SkillID]string{}
	for _, skill := range skills {
		label[skill.ID] = skill.Name
	}
	pairs := []string{}
	for _, mention := range mentions {
		pairs = append(pairs, label[mention.From]+" -> "+label[mention.To])
	}
	slices.Sort(pairs)
	return pairs
}

func requireMentionPairs(t *testing.T, got, want []string) {
	t.Helper()
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mentions =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestListMentionsDrawsInvocationFormsForAnyName(t *testing.T) {
	root := mentionSource(t, map[string]string{
		"entry": strings.Join([]string{
			"Run /ship when the work is done.",
			"Codex users type $deploy instead.",
			"Then invoke superpowers:brainstorm before planning.",
			`Call the Skill tool with "grill" to sharpen the plan.`,
			"Follow ../review/checklist.md for the review.",
			"See .claude/skills/audit/notes.md for the audit notes.",
			"Read lint/SKILL.md first.",
			"Treat the result as a plan, and spec it out.",
		}, "\n") + "\n",
		"ship": "# ship\n", "deploy": "# deploy\n", "brainstorm": "# brainstorm\n", "grill": "# grill\n",
		"review": "# review\n", "audit": "# audit\n", "lint": "# lint\n", "plan": "# plan\n", "spec": "# spec\n",
	})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)

	got := mentionPairs(t, lib, library.MentionsFilter{CollectionID: skills["entry"].CollectionID})
	requireMentionPairs(t, got, []string{
		"entry -> ship", "entry -> deploy", "entry -> brainstorm", "entry -> grill",
		"entry -> review", "entry -> audit", "entry -> lint",
	})
}

func TestListMentionsDrawsMultiWordNamesWrittenPlainly(t *testing.T) {
	root := mentionSource(t, map[string]string{
		"entry": strings.Join([]string{
			"Next, use writing-plans to write the plan.",
			"Or run `test-driven-development` with it.",
			"Writing-Plans and Code-Review are not names: the case differs.",
			"pre-code-review-step is a longer word, not the name.",
		}, "\n") + "\n",
		"writing-plans": "# writing-plans\n", "test-driven-development": "# tdd\n", "code-review": "# code-review\n",
	})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)

	mentions, err := lib.ListMentions(t.Context(), library.MentionsFilter{CollectionID: skills["entry"].CollectionID})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Mention{
		{From: skills["entry"].ID, To: skills["writing-plans"].ID, Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Next, use writing-plans to write the plan."}}},
		{From: skills["entry"].ID, To: skills["test-driven-development"].ID, Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Or run `test-driven-development` with it."}}},
	}
	slices.SortFunc(want, func(a, b library.Mention) int { return int(a.To - b.To) })
	if !reflect.DeepEqual(mentions, want) {
		t.Fatalf("ListMentions = %#v; want %#v", mentions, want)
	}
}

func TestListMentionsSkipsPathsURLsFileNamesAndSelf(t *testing.T) {
	root := mentionSource(t, map[string]string{
		"entry": strings.Join([]string{
			"This is entry; entry calls itself /entry.",
			"Copy docs/writing-plans/example.md into place.",
			"Upstream: https://example.com/skills/writing-plans/SKILL.md",
			"Open writing-plans.md for the template.",
			"See [the template](/review.md) or $review.md, not a mention.",
			"Notes live in docs/my-skills/writing-plans/example.md.",
			"The single word review here is just a word.",
		}, "\n") + "\n",
		"writing-plans": "# writing-plans\n", "review": "# review\n",
	})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)

	got := mentionPairs(t, lib, library.MentionsFilter{CollectionID: skills["entry"].CollectionID})
	requireMentionPairs(t, got, []string{})
}

func TestListMentionsReadsEveryMarkdownFileIncludingFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "entry", "SKILL.md"), []byte("---\nname: entry\ndescription: Use after writing-plans finishes.\n---\n# entry\n"), 0o644)
	writeFile(t, filepath.Join(root, "entry", "references", "flow.md"), []byte("# Flow\n\nThen hand off to /ship.\n"), 0o644)
	writeFile(t, filepath.Join(root, "entry", "notes.txt"), []byte("Also run code-review.\n"), 0o644)
	writeLocalSkill(t, filepath.Join(root, "writing-plans"), "# writing-plans\n")
	writeLocalSkill(t, filepath.Join(root, "ship"), "# ship\n")
	writeLocalSkill(t, filepath.Join(root, "code-review"), "# code-review\n")
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)

	mentions, err := lib.ListMentions(t.Context(), library.MentionsFilter{CollectionID: skills["entry"].CollectionID})
	if err != nil {
		t.Fatal(err)
	}
	lines := map[library.SkillID][]library.MentionLine{}
	for _, mention := range mentions {
		lines[mention.To] = mention.Lines
	}
	want := map[library.SkillID][]library.MentionLine{
		skills["writing-plans"].ID: {{Path: "SKILL.md", Text: "description: Use after writing-plans finishes."}},
		skills["ship"].ID:          {{Path: "references/flow.md", Text: "Then hand off to /ship."}},
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("mention lines = %#v; want %#v", lines, want)
	}
}

func TestListMentionsListsEachDistinctLineOnce(t *testing.T) {
	root := mentionSource(t, map[string]string{
		"entry":         "Use writing-plans.\n\n  Use writing-plans.\n\nAfter that, writing-plans again: /writing-plans.\n",
		"writing-plans": "# writing-plans\n",
	})
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)

	mentions, err := lib.ListMentions(t.Context(), library.MentionsFilter{CollectionID: skills["entry"].CollectionID})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.MentionLine{
		{Path: "SKILL.md", Text: "Use writing-plans."},
		{Path: "SKILL.md", Text: "After that, writing-plans again: /writing-plans."},
	}
	if len(mentions) != 1 || !reflect.DeepEqual(mentions[0].Lines, want) {
		t.Fatalf("ListMentions = %#v; want one mention with lines %#v", mentions, want)
	}
}

func TestListMentionsSkipsLinesSharedByMoreThanHalfTheCollection(t *testing.T) {
	preamble := "Only proactively suggest /ship and /qa."
	skills := map[string]string{
		"ship": "# ship\n", "qa": "# qa\n",
		"one":   preamble + "\n",
		"two":   preamble + "\n",
		"three": preamble + "\nWhen done, run /qa yourself.\n",
	}
	lib := openTestLibrary(t)
	fetched := fetchMentionSource(t, lib, mentionSource(t, skills))

	got := mentionPairs(t, lib, library.MentionsFilter{CollectionID: fetched["one"].CollectionID})
	requireMentionPairs(t, got, []string{"three -> qa"})
}

func TestListMentionsKeepsLinesSharedByHalfTheCollectionOrFewerThanThree(t *testing.T) {
	line := "Hand the result to /ship."
	lib := openTestLibrary(t)
	half := fetchMentionSource(t, lib, mentionSource(t, map[string]string{
		"ship": "# ship\n", "one": line + "\n", "two": line + "\n", "three": line + "\n", "four": "# four\n", "five": "# five\n",
	}))
	small := fetchMentionSource(t, lib, mentionSource(t, map[string]string{
		"deploy": "# deploy\n", "alpha": "Then /deploy.\n", "beta": "Then /deploy.\n",
	}))

	requireMentionPairs(t, mentionPairs(t, lib, library.MentionsFilter{CollectionID: half["one"].CollectionID}),
		[]string{"one -> ship", "two -> ship", "three -> ship"})
	requireMentionPairs(t, mentionPairs(t, lib, library.MentionsFilter{CollectionID: small["alpha"].CollectionID}),
		[]string{"alpha -> deploy", "beta -> deploy"})
}

func TestListMentionsReachesOtherCollectionsAndPrefersTheSameNameInThisSet(t *testing.T) {
	lib := openTestLibrary(t)
	home := fetchMentionSource(t, lib, mentionSource(t, map[string]string{
		"entry":       "Start with shared-tool, then other-thing.\n",
		"shared-tool": "# shared-tool here\n",
	}))
	away := fetchMentionSource(t, lib, mentionSource(t, map[string]string{
		"shared-tool": "# shared-tool away\n",
		"other-thing": "# other-thing\n",
	}))

	mentions, err := lib.ListMentions(t.Context(), library.MentionsFilter{CollectionID: home["entry"].CollectionID})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Mention{
		{From: home["entry"].ID, To: home["shared-tool"].ID, Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Start with shared-tool, then other-thing."}}},
		{From: home["entry"].ID, To: away["other-thing"].ID, Lines: []library.MentionLine{{Path: "SKILL.md", Text: "Start with shared-tool, then other-thing."}}},
	}
	if !reflect.DeepEqual(mentions, want) {
		t.Fatalf("collection mentions = %#v; want %#v", mentions, want)
	}

	group, err := lib.CreateGroup(t.Context(), "flow")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{home["entry"].ID, away["shared-tool"].ID} {
		if _, err := lib.AddSkillToGroup(t.Context(), group.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	mentions, err = lib.ListMentions(t.Context(), library.MentionsFilter{GroupID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	targets := []library.SkillID{}
	for _, mention := range mentions {
		targets = append(targets, mention.To)
	}
	if wantTargets := slices.Sorted(slices.Values([]library.SkillID{away["shared-tool"].ID, away["other-thing"].ID})); !reflect.DeepEqual(targets, wantTargets) {
		t.Fatalf("group mention targets = %v; want %v (the group's shared-tool and other-thing)", targets, wantTargets)
	}
}

func TestListMentionsInGroupSkipsSharedLinesByEachSkillsCollection(t *testing.T) {
	preamble := "Only proactively suggest /ship."
	lib := openTestLibrary(t)
	source := fetchMentionSource(t, lib, mentionSource(t, map[string]string{
		"ship": "# ship\n", "one": preamble + "\n", "two": preamble + "\n", "three": preamble + "\n",
	}))
	group, err := lib.CreateGroup(t.Context(), "picked")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "ship"} {
		if _, err := lib.AddSkillToGroup(t.Context(), group.ID, source[name].ID); err != nil {
			t.Fatal(err)
		}
	}

	requireMentionPairs(t, mentionPairs(t, lib, library.MentionsFilter{GroupID: group.ID}), []string{})
}

func TestListMentionsFollowsNewVersionsFetchesAndRemovals(t *testing.T) {
	lib := openTestLibrary(t)
	entry, source := fetchLocalSkill(t, lib, "entry", "Hand off to writing-plans.\n")
	filter := library.MentionsFilter{CollectionID: entry.CollectionID}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{})

	planner := fetchSkillWithBody(t, lib, "writing-plans", "# writing-plans\n")
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> writing-plans"})

	fetchSkillWithBody(t, lib, "code-review", "# code-review\n")
	writeLocalSkill(t, source, "Hand off to code-review.\n")
	if _, err := updateSkill(t, lib, t.Context(), entry.ID); err != nil {
		t.Fatal(err)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> code-review"})

	writeLocalSkill(t, source, "Hand off to code-review, then writing-plans.\n")
	if _, err := updateSkill(t, lib, t.Context(), entry.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(t.Context(), planner.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	requireMentionPairs(t, mentionPairs(t, lib, filter), []string{"entry -> code-review"})
}

func TestListMentionsRejectsBadFiltersAndMissingSets(t *testing.T) {
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "entry")
	group, err := lib.CreateGroup(t.Context(), "empty")
	if err != nil {
		t.Fatal(err)
	}

	for _, filter := range []library.MentionsFilter{{}, {CollectionID: skill.CollectionID, GroupID: group.ID}, {CollectionID: -1}} {
		_, err := lib.ListMentions(t.Context(), filter)
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "collection_id"})
	}
	for _, filter := range []library.MentionsFilter{{CollectionID: skill.CollectionID + 100}, {GroupID: group.ID + 100}} {
		_, err := lib.ListMentions(t.Context(), filter)
		requireLibraryError(t, err, library.NotFound)
	}
	mentions, err := lib.ListMentions(t.Context(), library.MentionsFilter{GroupID: group.ID})
	if err != nil || mentions == nil || len(mentions) != 0 {
		t.Fatalf("ListMentions(empty group) = %#v, %v; want empty list", mentions, err)
	}
}

func TestListMentionsMatchesNamesCaseSensitively(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, filepath.Join(root, "entry"), "Run /Ship, $SHIP, or WRITING-PLANS.\n")
	writeLocalSkill(t, filepath.Join(root, "ship"), "# ship\n")
	writeLocalSkill(t, filepath.Join(root, "writing-plans"), "# writing-plans\n")
	lib := openTestLibrary(t)
	skills := fetchMentionSource(t, lib, root)

	requireMentionPairs(t, mentionPairs(t, lib, library.MentionsFilter{CollectionID: skills["entry"].CollectionID}), []string{})
}
