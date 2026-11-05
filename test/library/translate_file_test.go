package library_test

import (
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

func fileTranslations(t *testing.T, lib *library.Library, version library.VersionID, path string) map[string]string {
	t.Helper()
	file, err := lib.GetVersionFile(t.Context(), version, path)
	if err != nil {
		t.Fatal(err)
	}
	return file.Translations
}

func currentVersion(t *testing.T, lib *library.Library, id library.SkillID) library.VersionID {
	t.Helper()
	skill, err := lib.GetSkill(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return skill.VersionID
}

// 翻译文件把文件全文作为用户消息发给模型,系统提示里写明文件名和要译成的语言;译文存进数据库,
// 之后读这个文件时按语言给出。再点一次翻译也重新翻译,覆盖原来的译文;别的语言另存一份。
func TestTranslateVersionFileStoresTranslation(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	skill, source := fetchLocalSkill(t, lib, "formatter", "# Formatter\n")
	writeFile(t, filepath.Join(source, "references", "guide.md"), []byte("# Guide\n\nRun the formatter.\n"), 0o644)
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	version := currentVersion(t, lib, skill.ID)

	stub.reply("# 指南\n\n运行格式化工具。\n")
	got, err := lib.TranslateVersionFile(t.Context(), version, "references/guide.md", "zh")
	if err != nil || got != "# 指南\n\n运行格式化工具。" {
		t.Fatalf("TranslateVersionFile = %q, %v; want the trimmed answer", got, err)
	}
	calls := stub.takeCalls()
	if len(calls) != 1 {
		t.Fatalf("%d requests, want 1", len(calls))
	}
	system, user := userText(calls[0])
	if user != "# Guide\n\nRun the formatter.\n" || !strings.Contains(system, `"references/guide.md"`) || !strings.Contains(system, "Simplified Chinese") {
		t.Fatalf("request = system %q, user %q; want the whole file translated into Simplified Chinese", system, user)
	}
	if got := fileTranslations(t, lib, version, "references/guide.md"); !maps.Equal(got, map[string]string{"zh": "# 指南\n\n运行格式化工具。"}) {
		t.Fatalf("translations = %v", got)
	}
	if got := fileTranslations(t, lib, version, "SKILL.md"); got != nil {
		t.Fatalf("translations of a file not translated = %v, want none", got)
	}

	stub.reply("# 使用指南")
	if _, err := lib.TranslateVersionFile(t.Context(), version, "references/guide.md", "zh"); err != nil {
		t.Fatal(err)
	}
	stub.reply("# Guide (en)")
	if _, err := lib.TranslateVersionFile(t.Context(), version, "references/guide.md", "en"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"zh": "# 使用指南", "en": "# Guide (en)"}
	if got := fileTranslations(t, lib, version, "references/guide.md"); !maps.Equal(got, want) {
		t.Fatalf("translations = %v, want %v", got, want)
	}
}

// 译文按文件内容存:技能更新后没改的文件、旧版本里的同一份内容、别的技能里内容相同的文件都带这份译文;
// 改过的文件没有,再翻译只换掉新内容的译文,旧版本的不变。
func TestFileTranslationFollowsContent(t *testing.T) {
	lib, stub := translatingLibrary(t, library.AIProviderOpenAI)
	skill, source := fetchLocalSkill(t, lib, "formatter", "# Formatter\n")
	first := currentVersion(t, lib, skill.ID)
	stub.reply("# 格式化")
	if _, err := lib.TranslateVersionFile(t.Context(), first, "SKILL.md", "zh"); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(source, "notes.md"), []byte("more\n"), 0o644)
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	second := currentVersion(t, lib, skill.ID)
	if second == first {
		t.Fatal("the update made no new version")
	}
	if got := fileTranslations(t, lib, second, "SKILL.md"); got["zh"] != "# 格式化" {
		t.Fatalf("translations of the unchanged SKILL.md = %v, want the stored one", got)
	}
	twin := fetchSkillWithBody(t, lib, "formatter", "# Formatter\n")
	if got := fileTranslations(t, lib, twin.VersionID, "SKILL.md"); got["zh"] != "# 格式化" {
		t.Fatalf("translations of the same content in another skill = %v, want the stored one", got)
	}

	writeLocalSkill(t, source, "# Formatter v2\n")
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	third := currentVersion(t, lib, skill.ID)
	if got := fileTranslations(t, lib, third, "SKILL.md"); got != nil {
		t.Fatalf("translations of the changed SKILL.md = %v, want none", got)
	}
	stub.reply("# 格式化 v2")
	if _, err := lib.TranslateVersionFile(t.Context(), third, "SKILL.md", "zh"); err != nil {
		t.Fatal(err)
	}
	if got := fileTranslations(t, lib, first, "SKILL.md"); got["zh"] != "# 格式化" {
		t.Fatalf("translations of the old version = %v; translating the new content must not change it", got)
	}
	if err := lib.RemoveSkill(t.Context(), skill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatalf("RemoveSkill with file translations = %v", err)
	}
}

// 没填模型返回 NotConfigured,语言不是语言标签返回 InvalidArgument,版本或路径不存在返回 NotFound,
// 二进制文件和只有空白的文件返回 FailedPrecondition(Details.Path),都不发请求。
// 服务拒绝、回答被截断时按测试连接的规则归类,不存译文,原来的译文不变。
func TestTranslateVersionFileErrors(t *testing.T) {
	stub := newAIStub(t)
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "formatter", "# Formatter\n")
	writeFile(t, filepath.Join(source, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0xff, 0xfe}, 0o644)
	writeFile(t, filepath.Join(source, "blank.md"), []byte(" \n\n"), 0o644)
	if _, err := updateSkill(t, lib, t.Context(), skill.ID); err != nil {
		t.Fatal(err)
	}
	version := currentVersion(t, lib, skill.ID)
	if _, err := lib.TranslateVersionFile(t.Context(), version, "SKILL.md", "zh"); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("TranslateVersionFile without a model = %v, want NotConfigured", err)
	}
	setAISettings(t, lib, library.AISettings{BaseURL: stub.url, APIKey: "sk-test", Model: "gpt-test"})
	if _, err := lib.TranslateVersionFile(t.Context(), version, "SKILL.md", "zh_CN"); !isFieldError(err, "language") {
		t.Errorf("TranslateVersionFile(zh_CN) = %v, want InvalidArgument on language", err)
	}
	if _, err := lib.TranslateVersionFile(t.Context(), 9999, "SKILL.md", "zh"); !library.IsCode(err, library.NotFound) {
		t.Errorf("TranslateVersionFile of a missing version = %v, want NotFound", err)
	}
	if _, err := lib.TranslateVersionFile(t.Context(), version, "missing.md", "zh"); !library.IsCode(err, library.NotFound) || library.ErrorOf(err).Details.Path != "missing.md" {
		t.Errorf("TranslateVersionFile of a missing path = %v, want NotFound with the path", err)
	}
	for _, path := range []string{"logo.png", "blank.md"} {
		if _, err := lib.TranslateVersionFile(t.Context(), version, path, "zh"); !library.IsCode(err, library.FailedPrecondition) || library.ErrorOf(err).Details.Path != path {
			t.Errorf("TranslateVersionFile(%s) = %v, want FailedPrecondition with the path", path, err)
		}
	}
	if got := stub.take(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none", got)
	}

	stub.reply("# 格式化")
	if _, err := lib.TranslateVersionFile(t.Context(), version, "SKILL.md", "zh"); err != nil {
		t.Fatal(err)
	}
	stub.truncate(true)
	if _, err := lib.TranslateVersionFile(t.Context(), version, "SKILL.md", "zh"); !isFieldError(err, "max_tokens") {
		t.Errorf("TranslateVersionFile cut off = %v, want InvalidArgument on max_tokens", err)
	}
	stub.truncate(false)
	stub.respond(http.StatusUnauthorized)
	if _, err := lib.TranslateVersionFile(t.Context(), version, "SKILL.md", "zh"); !library.IsCode(err, library.Permission) {
		t.Errorf("TranslateVersionFile rejected by the service = %v, want Permission", err)
	}
	if got := fileTranslations(t, lib, version, "SKILL.md"); got["zh"] != "# 格式化" {
		t.Fatalf("translations = %v; a failed translation must keep the old one", got)
	}
}
