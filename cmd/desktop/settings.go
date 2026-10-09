package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

const settingsFileName = "settings.json"

// Settings 是只属于这台机器的应用设置：存在用户配置目录下的 settings.json,不进技能库，也不随同步走。
// 加设置项就是加字段;文件里没有的字段取 defaultSettings 的值。
type Settings struct {
	AutoCheck     bool          `json:"auto_check"`       // 自动检查更新
	CheckInterval CheckInterval `json:"check_interval"`   // 自动检查的间隔
	StayInMenuBar bool          `json:"stay_in_menu_bar"` // 关闭窗口后留在菜单栏;关掉时关闭窗口就退出
	DockBadge     bool          `json:"dock_badge"`       // Dock 角标显示有新版本的技能数
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

func defaultSettings() Settings {
	return Settings{AutoCheck: true, CheckInterval: CheckDaily, StayInMenuBar: true, DockBadge: true}
}

func loadSettings(path string) (Settings, error) {
	settings := defaultSettings()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("read settings: %w", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return defaultSettings(), fmt.Errorf("parse settings %s: %w", path, err)
	}
	if !settings.CheckInterval.valid() {
		settings.CheckInterval = defaultSettings().CheckInterval
	}
	return settings, nil
}

func saveSettings(path string, settings Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), settingsFileName+".*")
	if err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
