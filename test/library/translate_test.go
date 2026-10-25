package library_test

import (
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// fetchDescribedSkill 从独立的本地来源入库一个指定描述的技能,返回来源目录,供之后改描述再更新。
func fetchDescribedSkill(t *testing.T, lib *library.Library, name, description string) (library.Skill, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeDescription(t, source, description)
	skill, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	return skill, source
}

func writeDescription(t *testing.T, source, description string) {
	t.Helper()
	document := "---\nname: " + filepath.Base(source) + "\ndescription: " + description + "\n---\n# body\n"
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte(document), 0o644)
}

func translationsOf(t *testing.T, lib *library.Library, id library.SkillID) map[string]string {
	t.Helper()
	skill, err := lib.GetSkill(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return skill.DescriptionTranslations
}

// userText 取出请求里发给模型的用户消息与系统提示(OpenAI 兼容的接口)。
func userText(call aiCall) (system, user string) {
	messages, _ := call.body["messages"].([]any)
	for _, m := range messages {
		message, _ := m.(map[string]any)
		content, _ := message["content"].(string)
		switch message["role"] {
		case "system":
			system = content
		case "user":
			user = content
		}
	}
	return system, user
}

func translatingLibrary(t *testing.T, provider library.AIProvider) (*library.Library, *aiStub) {
	t.Helper()
	stub := newAIStub(t)
	lib := openTestLibrary(t)
	settings := library.AISettings{Provider: provider, BaseURL: stub.url + "/v1", APIKey: "sk-test", Model: "model-test"}
	if provider == library.AIProviderAnthropic {
		settings.BaseURL = stub.url
	}
	setAISettings(t, lib, settings)
	return lib, stub
}

// 翻译描述用数据库里的 AI 设置把当前描述发给模型,让它译成语言标签对应的语言;译文存进数据库,
// 之后读技能时按语言给出。再点一次翻译也重新翻译,覆盖原来的译文。
func TestTranslateDescriptionStoresTranslation(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	skill, _ := fetchDescribedSkill(t, lib, "formatter", "Format code before committing")
	stub.reply(" 提交前格式化代码\n")
	got, err := lib.TranslateDescription(t.Context(), skill.ID, "zh")
	if err != nil || got != "提交前格式化代码" {
		t.Fatalf("TranslateDescription = %q, %v; want the trimmed answer", got, err)
	}
	calls := stub.takeCalls()
	if len(calls) != 1 {
		t.Fatalf("%d requests, want 1", len(calls))
	}
	system, user := userText(calls[0])
	if user != "Format code before committing" || !strings.Contains(system, "Simplified Chinese") || calls[0].model != "model-test" {
		t.Fatalf("request = system %q, user %q, model %q; want the description translated into Simplified Chinese", system, user, calls[0].model)
	}
	if got := translationsOf(t, lib, skill.ID); !maps.Equal(got, map[string]string{"zh": "提交前格式化代码"}) {
		t.Fatalf("translations = %v", got)
	}

	stub.reply("在提交之前格式化代码")
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); err != nil {
		t.Fatal(err)
	}
	stub.reply("Format code before committing (en)")
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "en"); err != nil {
		t.Fatal(err)
	}
	skills, err := lib.ListSkills(t.Context())
	if err != nil || len(skills) != 1 {
		t.Fatalf("ListSkills = %v, %v", skills, err)
	}
	want := map[string]string{"zh": "在提交之前格式化代码", "en": "Format code before committing (en)"}
	if got := skills[0].DescriptionTranslations; !maps.Equal(got, want) {
		t.Fatalf("translations = %v, want %v", got, want)
	}
}

// Anthropic 的接口:描述作为用户消息、翻译要求作为系统提示;回答里的思考块不算译文。
// 推理模型把思考写在 <think> 里的,也去掉。
func TestTranslateDescriptionTakesOnlyAnswerText(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderAnthropic)
	skill, _ := fetchDescribedSkill(t, lib, "formatter", "Format code")
	stub.reply("格式化代码")
	if got, err := lib.TranslateDescription(t.Context(), skill.ID, "zh-Hant"); err != nil || got != "格式化代码" {
		t.Fatalf("TranslateDescription = %q, %v", got, err)
	}
	call := stub.takeCalls()[0]
	system, _ := call.body["system"].([]any)
	if len(system) != 1 || !strings.Contains(system[0].(map[string]any)["text"].(string), "Traditional Chinese") {
		t.Fatalf("system = %#v, want the translation instruction for Traditional Chinese", call.body["system"])
	}

	stub.reply("<think>the user wants Chinese</think>\n格式化代码。")
	if got, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); err != nil || got != "格式化代码。" {
		t.Fatalf("TranslateDescription = %q, %v; want the thinking removed", got, err)
	}
	stub.reply("<think>only thinking</think>")
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("TranslateDescription with no answer text = %v, want InvalidArgument", err)
	}
	if got := translationsOf(t, lib, skill.ID); got["zh"] != "格式化代码。" {
		t.Fatalf("translations = %v; a failed translation must keep the old one", got)
	}
}

// 技能更新后描述变了,旧译文对不上当前描述,读技能时不再给出;再翻译就是新描述的译文。描述没变的更新不影响译文。
func TestTranslationIsDroppedWhenDescriptionChanges(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	skill, source := fetchDescribedSkill(t, lib, "formatter", "Format code")
	stub.reply("格式化代码")
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "notes.md"), []byte("more\n"), 0o644)
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	if got := translationsOf(t, lib, skill.ID); got["zh"] != "格式化代码" {
		t.Fatalf("translations after an update keeping the description = %v", got)
	}

	writeDescription(t, source, "Format and lint code")
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	if got := translationsOf(t, lib, skill.ID); got != nil {
		t.Fatalf("translations after the description changed = %v, want none", got)
	}
	stub.reply("格式化并检查代码")
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); err != nil {
		t.Fatal(err)
	}
	if got := translationsOf(t, lib, skill.ID); got["zh"] != "格式化并检查代码" {
		t.Fatalf("translations = %v", got)
	}
}

// 自动翻译只翻还没有译文的:跳过描述为空的、已有对得上当前描述的译文的,以及不在 ids 里的,这些都不发请求;
// 返回这次翻译了几个。界面语言是中文时,已经是中文的描述也跳过;其他语言都交给模型判断。
func TestTranslateDescriptionsSkipsWhatNeedsNoTranslation(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	english, _ := fetchDescribedSkill(t, lib, "english", "Format code")
	chinese, _ := fetchDescribedSkill(t, lib, "chinese", "格式化 Go 代码")
	empty, _ := fetchDescribedSkill(t, lib, "empty", `""`)
	done, _ := fetchDescribedSkill(t, lib, "done", "Lint code")
	other, _ := fetchDescribedSkill(t, lib, "other", "Not asked")
	stub.reply("检查代码")
	if _, err := lib.TranslateDescription(t.Context(), done.ID, "zh"); err != nil {
		t.Fatal(err)
	}
	stub.takeCalls()

	stub.reply("格式化代码")
	ids := []library.SkillID{english.ID, chinese.ID, empty.ID, done.ID, 9999}
	if got, err := lib.TranslateDescriptions(t.Context(), "zh", ids); err != nil || got.Translated != 1 || got.Failed != nil {
		t.Fatalf("TranslateDescriptions(zh) = %+v, %v; want 1 translated", got, err)
	}
	if calls := stub.takeCalls(); len(calls) != 1 {
		t.Fatalf("%d requests, want 1 for the English description", len(calls))
	}
	if got := translationsOf(t, lib, english.ID); got["zh"] != "格式化代码" {
		t.Fatalf("translations of english = %v", got)
	}
	if got := translationsOf(t, lib, other.ID); got != nil {
		t.Fatalf("translations of a skill not asked for = %v", got)
	}
	if got, err := lib.TranslateDescriptions(t.Context(), "zh", ids); err != nil || got.Translated != 0 {
		t.Fatalf("TranslateDescriptions(zh) again = %+v, %v; want nothing left", got, err)
	}

	stub.reply("Format Go code")
	if got, err := lib.TranslateDescriptions(t.Context(), "en", ids); err != nil || got.Translated != 3 {
		t.Fatalf("TranslateDescriptions(en) = %+v, %v; want every non-empty description sent to the model", got, err)
	}
	if n := len(stub.takeCalls()); n != 3 {
		t.Fatalf("%d requests, want 3", n)
	}
}

// 中文界面下按汉字在汉字与其他文字的词里占的比例判断描述是不是中文:中文里夹几个英文词的算中文、跳过;
// 英文里夹一个中文专名的、日文(有假名)都要翻译。
func TestTranslateDescriptionsJudgesChineseByShare(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	stub.reply("译文")
	want := map[string]bool{
		"用 React 和 TypeScript 构建前端组件": false,
		"格式化代码":                            false,
		"Use when writing 飞书 docs":         true,
		"Generate 小红书 posts for a brand":   true,
		"コードを整形する":                         true,
		"Formater le code avant le commit": true,
	}
	var ids []library.SkillID
	byID := map[library.SkillID]string{}
	i := 0
	for description := range want {
		i++
		skill, _ := fetchDescribedSkill(t, lib, "skill-"+string(rune('a'+i)), description)
		ids = append(ids, skill.ID)
		byID[skill.ID] = description
	}
	if _, err := lib.TranslateDescriptions(t.Context(), "zh", ids); err != nil {
		t.Fatal(err)
	}
	for id, description := range byID {
		if got := translationsOf(t, lib, id)["zh"] != ""; got != want[description] {
			t.Errorf("%q translated = %v, want %v", description, got, want[description])
		}
	}
}

// 被 max_tokens 截断的回答(OpenAI 兼容的 finish_reason 为 length,Anthropic 的 stop_reason 为 max_tokens),
// 以及停在 <think> 里的回答都不存:报 InvalidArgument,Details.Field 为 max_tokens,原来的译文不变。测试连接也一样。
func TestTranslateDescriptionRejectsTruncatedAnswers(t *testing.T) {
	for _, provider := range []library.AIProvider{library.AIProviderOpenAI, library.AIProviderAnthropic} {
		lib, stub := translatingLibrary(t, provider)
		skill, _ := fetchDescribedSkill(t, lib, "formatter", "Format code")
		stub.reply("格式化代码")
		if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); err != nil {
			t.Fatal(err)
		}
		stub.reply("格式")
		stub.truncate(true)
		if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); !isFieldError(err, "max_tokens") {
			t.Errorf("%s: TranslateDescription cut off = %v, want InvalidArgument on max_tokens", provider, err)
		}
		settings, err := lib.AISettings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.TestAISettings(t.Context(), settings); !isFieldError(err, "max_tokens") {
			t.Errorf("%s: TestAISettings cut off = %v, want InvalidArgument on max_tokens", provider, err)
		}
		stub.truncate(false)
		stub.reply("<think>the user wants Chinese, so")
		if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); !isFieldError(err, "max_tokens") {
			t.Errorf("%s: TranslateDescription stopping inside <think> = %v, want InvalidArgument on max_tokens", provider, err)
		}
		if got := translationsOf(t, lib, skill.ID); got["zh"] != "格式化代码" {
			t.Errorf("%s: translations = %v; a cut-off answer must not replace the old one", provider, got)
		}
	}
}

// 自动翻译里只因某一条描述失败的(服务拒绝了这一条、回答被截断)记进 Failed,其余照常翻译,不返回错误;
// 整体性的失败(如 Key 被拒)返回错误。
func TestTranslateDescriptionsSkipsDescriptionsThatFail(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	var ids []library.SkillID
	var blocked library.SkillID
	for _, name := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"} {
		description := "Describe " + name
		if name == "charlie" {
			description = "Something the filter blocks"
		}
		skill, _ := fetchDescribedSkill(t, lib, name, description)
		ids = append(ids, skill.ID)
		if name == "charlie" {
			blocked = skill.ID
		}
	}
	stub.reply("译文")
	stub.rejectContaining("the filter blocks")
	got, err := lib.TranslateDescriptions(t.Context(), "zh", ids)
	if err != nil || got.Translated != 5 || len(got.Failed) != 1 || got.Failed[0].SkillID != blocked || got.Failed[0].Error.Code != library.InvalidArgument {
		t.Fatalf("TranslateDescriptions = %+v, %v; want 5 translated and charlie failed with InvalidArgument", got, err)
	}
	if translationsOf(t, lib, blocked) != nil {
		t.Fatal("the rejected description must have no translation")
	}

	stub.rejectContaining("")
	stub.respond(http.StatusUnauthorized)
	if got, err := lib.TranslateDescriptions(t.Context(), "zh", ids); !library.IsCode(err, library.Permission) || got.Translated != 0 {
		t.Fatalf("TranslateDescriptions with the key rejected = %+v, %v; want Permission", got, err)
	}
}

// 没填模型时翻译返回 NotConfigured,语言不是语言标签返回 InvalidArgument,技能不存在返回 NotFound,
// 描述为空返回 FailedPrecondition,都不发请求。服务拒绝时按测试连接的规则归类,不存译文;
// 自动翻译遇到失败也返回这个错误。
func TestTranslateDescriptionErrors(t *testing.T) {
	stub := newAIStub(t)
	lib := openTestLibrary(t)
	skill, _ := fetchDescribedSkill(t, lib, "formatter", "Format code")
	empty, _ := fetchDescribedSkill(t, lib, "empty", `""`)
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("TranslateDescription without a model = %v, want NotConfigured", err)
	}
	if _, err := lib.TranslateDescriptions(t.Context(), "zh", []library.SkillID{skill.ID}); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("TranslateDescriptions without a model = %v, want NotConfigured", err)
	}
	setAISettings(t, lib, library.AISettings{BaseURL: stub.url, APIKey: "sk-test", Model: "gpt-test"})
	for _, language := range []string{"", "简体中文", "zh_CN", "zh CN", "z"} {
		if _, err := lib.TranslateDescription(t.Context(), skill.ID, language); !isFieldError(err, "language") {
			t.Errorf("TranslateDescription(%q) = %v, want InvalidArgument on language", language, err)
		}
	}
	if _, err := lib.TranslateDescription(t.Context(), 9999, "zh"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("TranslateDescription of a missing skill = %v, want NotFound", err)
	}
	if _, err := lib.TranslateDescription(t.Context(), empty.ID, "zh"); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("TranslateDescription of an empty description = %v, want FailedPrecondition", err)
	}
	if got := stub.take(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none", got)
	}

	stub.respond(http.StatusUnauthorized)
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); !library.IsCode(err, library.Permission) {
		t.Fatalf("TranslateDescription rejected by the service = %v, want Permission", err)
	}
	if got, err := lib.TranslateDescriptions(t.Context(), "zh", []library.SkillID{skill.ID}); got.Translated != 0 || !library.IsCode(err, library.Permission) {
		t.Fatalf("TranslateDescriptions rejected by the service = %+v, %v; want nothing translated and Permission", got, err)
	}
	if got := translationsOf(t, lib, skill.ID); got != nil {
		t.Fatalf("translations = %v, want none after failures", got)
	}
}

// 译文随技能一并删除:移除有译文的技能不受影响。
func TestRemoveSkillWithTranslation(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	skill, _ := fetchDescribedSkill(t, lib, "formatter", "Format code")
	stub.reply("格式化代码")
	if _, err := lib.TranslateDescription(t.Context(), skill.ID, "zh"); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(t.Context(), skill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatalf("RemoveSkill = %v", err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills = %v, %v; want empty", skills, err)
	}
}
