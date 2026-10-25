package main

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// Settings 是设置页里的应用设置。WSLDistro 存在技能库里、只属于这台机器(Library.SetWSLDistro);
// 其余各项是技能库数据库里的应用设置(Library.AppSettings),和库里其他内容一样随推送、拉回走。
// 加设置项就是加字段并在 appValues / settingsFrom 里加一项;数据库里没有的项取 defaultSettings 的值。
type Settings struct {
	AutoCheck     bool          `json:"auto_check"`         // 自动检查更新
	CheckInterval CheckInterval `json:"check_interval"`     // 自动检查的间隔
	StayInMenuBar bool          `json:"stay_in_menu_bar"`   // 关闭窗口后留在菜单栏;关掉时关闭窗口就退出
	DockBadge     bool          `json:"dock_badge"`         // Dock 角标显示计入角标的技能数(技能库的 UpdateCheck.Pending)
	Language      Language      `json:"language,omitempty"` // 界面语言;空字符串跟随系统
	// WSLDistro 是各 Agent 的全局目录所在的 WSL 发行版;空字符串为这台机器本身(Windows 本机)。只在 Windows 上可选。
	WSLDistro string `json:"wsl_distro,omitempty"`
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

const firstOpenDoneKey = "first_open_done"

func defaultSettings() Settings {
	return Settings{AutoCheck: true, CheckInterval: CheckDaily, StayInMenuBar: true, DockBadge: true}
}

func (s Settings) appValues(firstOpenDone bool) map[string]string {
	values := map[string]string{
		"auto_check":       strconv.FormatBool(s.AutoCheck),
		"check_interval":   string(s.CheckInterval),
		"stay_in_menu_bar": strconv.FormatBool(s.StayInMenuBar),
		"dock_badge":       strconv.FormatBool(s.DockBadge),
		"language":         string(s.Language),
	}
	if len(s.AgentsShown) > 0 {
		shown, _ := json.Marshal(s.AgentsShown)
		values["agents_shown"] = string(shown)
	}
	if firstOpenDone {
		values[firstOpenDoneKey] = "true"
	}
	return values
}

func settingsFrom(values map[string]string) (Settings, bool) {
	s := defaultSettings()
	flag := func(name string, field *bool) {
		if v, err := strconv.ParseBool(values[name]); err == nil {
			*field = v
		}
	}
	flag("auto_check", &s.AutoCheck)
	flag("stay_in_menu_bar", &s.StayInMenuBar)
	flag("dock_badge", &s.DockBadge)
	if interval := CheckInterval(values["check_interval"]); interval.valid() {
		s.CheckInterval = interval
	}
	if language := Language(values["language"]); language.valid() {
		s.Language = language
	}
	var shown map[library.AgentCode]bool
	if json.Unmarshal([]byte(values["agents_shown"]), &shown) == nil && len(shown) > 0 {
		s.AgentsShown = shown
	}
	done, _ := strconv.ParseBool(values[firstOpenDoneKey])
	return s, done
}
