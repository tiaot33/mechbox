package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/cmd/desktop/internal/system"
	"github.com/tiaot33/mechbox/internal/library"
)

// Settings 是设置页里的应用设置。WSLDistro 存在技能库里、只属于这台机器(Library.SetWSLDistro);
// 其余各项是技能库数据库里的应用设置(Library.AppSettings),和库里其他内容一样随推送、拉回走。
// 加设置项就是加字段并在 appValues / settingsFrom 里加一项;数据库里没有的项取 defaultSettings 的值。
// 去掉的设置项(如原来的毛玻璃浓度 glass_tint、glass_tint_dark)留在旧数据库里时读的时候不认，下次保存整组替换时就没了。
type Settings struct {
	AutoCheck     bool          `json:"auto_check"`           // 自动检查更新
	CheckInterval CheckInterval `json:"check_interval"`       // 自动检查的间隔
	StayInMenuBar bool          `json:"stay_in_menu_bar"`     // 关闭窗口后留在菜单栏;关掉时关闭窗口就退出
	DockBadge     bool          `json:"dock_badge"`           // Dock 角标显示计入角标的技能数(技能库的 UpdateCheck.Pending)
	AutoTranslate bool          `json:"auto_translate"`       // 填了 AI 设置时，自动把技能描述翻译成界面语言(Library.TranslateDescriptions)
	Language      Language      `json:"language,omitempty"`   // 界面语言;空字符串跟随系统
	Appearance    Appearance    `json:"appearance,omitempty"` // 外观(浅色、深色);空字符串跟随系统
	// WSLDistro 是各 Agent 的全局目录所在的 WSL 发行版;空字符串为这台机器本身(Windows 本机)。只在 Windows 上可选。
	WSLDistro string `json:"wsl_distro,omitempty"`
	// FoldCollections 把左栏「集合」里技能少于 FoldBelow 个的集合收进最后一行，点开再列出;关掉时每个集合都列出。
	FoldCollections bool `json:"fold_collections"`
	// FoldBelow 是左栏折起来的集合的技能数上限(不含),取值 2 到 10,默认 3。
	FoldBelow int `json:"fold_below"`
	// AgentsShown 记下用户勾选过的 Agent 显示与否，按 Agent 代码;没勾选过的 Agent 按默认：检测到的和自定义的显示，其余不显示。
	// 显示只影响选择安装位置的地方，已经装了技能的 Agent 装出去的地方照常列出。
	AgentsShown map[library.AgentCode]bool `json:"agents_shown,omitempty"`
}

// CheckInterval 是自动检查更新的间隔。
type CheckInterval string

const (
	CheckOnOpen CheckInterval = "open"   // 只在打开时：启动应用、从菜单栏或 Dock 重新打开窗口时
	CheckHourly CheckInterval = "hourly" // 每小时
	CheckDaily  CheckInterval = "daily"  // 每天
	CheckWeekly CheckInterval = "weekly" // 每周
)

func (i CheckInterval) period() (time.Duration, bool) {
	switch i {
	case CheckHourly:
		return time.Hour, true
	case CheckDaily:
		return 24 * time.Hour, true
	case CheckWeekly:
		return 7 * 24 * time.Hour, true
	}
	return 0, false
}

func (i CheckInterval) valid() bool {
	_, timed := i.period()
	return timed || i == CheckOnOpen
}

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
	return languageOf(system.PreferredLanguage())
}

// languageOf 按语言标签(如 zh-Hans-CN、zh_TW.UTF-8、en-US)定界面语言：中文(含繁体)用中文，其余用英文。
func languageOf(tag string) Language {
	tag = strings.ToLower(tag)
	if tag == "zh" || strings.HasPrefix(tag, "zh-") || strings.HasPrefix(tag, "zh_") {
		return LanguageZh
	}
	return LanguageEn
}

// Appearance 是界面外观的设置值。
type Appearance string

const (
	AppearanceSystem Appearance = ""      // 跟随系统
	AppearanceLight  Appearance = "light" // 浅色
	AppearanceDark   Appearance = "dark"  // 深色
)

func (a Appearance) valid() bool {
	return a == AppearanceSystem || a == AppearanceLight || a == AppearanceDark
}

func (a Appearance) native() system.Appearance {
	switch a {
	case AppearanceLight:
		return system.AppearanceLight
	case AppearanceDark:
		return system.AppearanceDark
	}
	return system.AppearanceSystem
}

const (
	minFoldBelow = 2
	maxFoldBelow = 10
)

func foldBelowValid(n int) bool {
	return n >= minFoldBelow && n <= maxFoldBelow
}

const (
	firstOpenDoneKey    = "first_open_done"
	agentPoolOfferedKey = "agent_pool_offered"
)

// openedMarks 记着这个数据库在 app 里已经做过的一次性的事:firstOpen 是第一次打开的流程,agentPool 是按检测结果提示添加 Agent
// (第一次打开时随之做过;从旧版本升级上来的数据库还没做过,升级后第一次打开时提示一次)。
type openedMarks struct {
	firstOpen bool
	agentPool bool
}

func defaultSettings() Settings {
	return Settings{AutoCheck: true, CheckInterval: CheckDaily, StayInMenuBar: true, DockBadge: true, AutoTranslate: true, FoldBelow: 3}
}

func (s Settings) appValues(marks openedMarks) map[string]string {
	values := map[string]string{
		"auto_check":       strconv.FormatBool(s.AutoCheck),
		"check_interval":   string(s.CheckInterval),
		"stay_in_menu_bar": strconv.FormatBool(s.StayInMenuBar),
		"dock_badge":       strconv.FormatBool(s.DockBadge),
		"auto_translate":   strconv.FormatBool(s.AutoTranslate),
		"language":         string(s.Language),
		"appearance":       string(s.Appearance),
		"fold_collections": strconv.FormatBool(s.FoldCollections),
		"fold_below":       strconv.Itoa(s.FoldBelow),
	}
	if len(s.AgentsShown) > 0 {
		shown, _ := json.Marshal(s.AgentsShown)
		values["agents_shown"] = string(shown)
	}
	if marks.firstOpen {
		values[firstOpenDoneKey] = "true"
	}
	if marks.agentPool {
		values[agentPoolOfferedKey] = "true"
	}
	return values
}

func settingsFrom(values map[string]string) (Settings, openedMarks) {
	s := defaultSettings()
	flag := func(name string, field *bool) {
		if v, err := strconv.ParseBool(values[name]); err == nil {
			*field = v
		}
	}
	flag("auto_check", &s.AutoCheck)
	flag("stay_in_menu_bar", &s.StayInMenuBar)
	flag("dock_badge", &s.DockBadge)
	flag("auto_translate", &s.AutoTranslate)
	flag("fold_collections", &s.FoldCollections)
	if n, err := strconv.Atoi(values["fold_below"]); err == nil && foldBelowValid(n) {
		s.FoldBelow = n
	}
	if interval := CheckInterval(values["check_interval"]); interval.valid() {
		s.CheckInterval = interval
	}
	if language := Language(values["language"]); language.valid() {
		s.Language = language
	}
	if appearance := Appearance(values["appearance"]); appearance.valid() {
		s.Appearance = appearance
	}
	var shown map[library.AgentCode]bool
	if json.Unmarshal([]byte(values["agents_shown"]), &shown) == nil && len(shown) > 0 {
		s.AgentsShown = shown
	}
	var marks openedMarks
	marks.firstOpen, _ = strconv.ParseBool(values[firstOpenDoneKey])
	marks.agentPool, _ = strconv.ParseBool(values[agentPoolOfferedKey])
	return s, marks
}
