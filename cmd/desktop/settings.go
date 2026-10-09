package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// settingsFileName 是应用设置文件，与技能库放在同一个目录下。
const settingsFileName = "settings.json"

// Settings 是只属于这台机器的应用设置：存在用户配置目录下的 settings.json,不进技能库，也不随同步走。
// 加设置项就是加字段;文件里没有的字段取 defaultSettings 的值。
type Settings struct {
	AutoCheck     bool          `json:"auto_check"`       // 自动检查更新
	CheckInterval CheckInterval `json:"check_interval"`   // 自动检查的间隔
	StayInMenuBar bool          `json:"stay_in_menu_bar"` // 关闭窗口后留在菜单栏;关掉时关闭窗口就退出
	DockBadge     bool          `json:"dock_badge"`       // Dock 角标显示有新版本的技能数
}

// CheckInterval 是自动检查更新的间隔。
type CheckInterval string

const (
	CheckOnOpen CheckInterval = "open"   // 只在打开时：启动应用、从菜单栏或 Dock 重新打开窗口时
	CheckHourly CheckInterval = "hourly" // 每小时
	CheckDaily  CheckInterval = "daily"  // 每天
	CheckWeekly CheckInterval = "weekly" // 每周
)

// period 返回按时间检查的间隔;「只在打开时」不按时间，返回 false。
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

// loadSettings 读取设置文件;文件不存在时是默认设置。
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

// saveSettings 先写临时文件再改名，写到一半退出也不会留下残缺的设置文件。
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
