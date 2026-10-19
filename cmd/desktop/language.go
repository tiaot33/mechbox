package main

import "strings"

// Language 是界面语言的设置值。
type Language string

const (
	LanguageSystem Language = ""   // 跟随系统
	LanguageZh     Language = "zh" // 简体中文
	LanguageEn     Language = "en" // English
)

func (l Language) valid() bool {
	return l == LanguageSystem || l == LanguageZh || l == LanguageEn
}

func (l Language) resolve() Language {
	if l != LanguageSystem {
		return l
	}
	return languageOf(systemLanguage())
}

// languageOf 按语言标签(如 zh-Hans-CN、zh_TW.UTF-8、en-US)定界面语言：中文(含繁体)用中文，其余用英文。
func languageOf(tag string) Language {
	tag = strings.ToLower(tag)
	if tag == "zh" || strings.HasPrefix(tag, "zh-") || strings.HasPrefix(tag, "zh_") {
		return LanguageZh
	}
	return LanguageEn
}
