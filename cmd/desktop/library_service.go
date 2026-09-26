package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/skills-tui/internal/library"
)

// appDirName 是技能库在用户配置目录下的子目录名。
const appDirName = "skills-manager"

// defaultLibraryPath 返回真实技能库的位置：<用户配置目录>/skills-manager/library.db。
// macOS 上即 ~/Library/Application Support/skills-manager/library.db;
// 从 Finder / Dock 启动时也可用，因为它只依赖 HOME 而不依赖 shell 环境变量。
func defaultLibraryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, appDirName, "library.db"), nil
}

// LibraryService 是前端调用技能库的 Wails 绑定服务。它持有进程内唯一的 Library 实例，
// 方法沿用技能库的 ctx 优先、(T, error) 签名，ctx 随前端 promise.cancel() 取消。
type LibraryService struct {
	path string
	lib  library.Library
}

// NewLibraryService 创建绑定服务;库在 Wails 启动服务时才打开。
func NewLibraryService(path string) *LibraryService {
	return &LibraryService{path: path}
}

// ServiceStartup 在 Wails 启动服务时打开技能库：目录不存在则创建，库文件不存在则由 Open 初始化空库。
func (s *LibraryService) ServiceStartup(context.Context, application.ServiceOptions) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create library directory: %w", err)
	}
	lib, err := library.Open(s.path)
	if err != nil {
		return err
	}
	s.lib = lib
	return nil
}

// ServiceShutdown 在应用退出时关闭技能库。
func (s *LibraryService) ServiceShutdown() error {
	if s.lib == nil {
		return nil
	}
	return s.lib.Close()
}

// AppInfo 是界面展示用的环境信息。
type AppInfo struct {
	LibraryPath string `json:"library_path"` // 当前打开的技能库文件
	HomeDir     string `json:"home_dir"`     // 用户主目录，界面据此把路径缩写成 ~
}

// Info 返回技能库路径与用户主目录。
func (s *LibraryService) Info() (AppInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return AppInfo{}, fmt.Errorf("find home directory: %w", err)
	}
	return AppInfo{LibraryPath: s.path, HomeDir: home}, nil
}

// ConfigStatus 报告技能库依赖的外部配置是否就绪。这些配置目前只能来自环境变量;
// 从 Finder / Dock 启动的应用读不到 shell 里设置的环境变量(运行时配置另有 issue)。
type ConfigStatus struct {
	Sync        bool `json:"sync"`         // S3 同步:bucket 与 AWS 凭证齐全
	StoreURL    bool `json:"store_url"`    // 商店来源:SKILLS_STORE_URL
	GitHubToken bool `json:"github_token"` // GitHub API 令牌:GITHUB_TOKEN / GH_TOKEN(未设置时匿名访问，易被限流)
}

// Config 返回外部配置的就绪状态，只看是否设置，不校验取值。
func (s *LibraryService) Config() ConfigStatus {
	set := func(names ...string) bool {
		for _, name := range names {
			if os.Getenv(name) == "" {
				return false
			}
		}
		return true
	}
	return ConfigStatus{
		Sync:        set("SKILLS_S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"),
		StoreURL:    set("SKILLS_STORE_URL"),
		GitHubToken: set("GITHUB_TOKEN") || set("GH_TOKEN"),
	}
}
