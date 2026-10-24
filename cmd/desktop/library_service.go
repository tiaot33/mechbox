package main

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/mechbox/internal/library"
)

// appDirName 是技能库在用户配置目录下的子目录名。
const appDirName = "mechbox"

// defaultLibraryPath 返回真实技能库的位置：<用户配置目录>/mechbox/library.db。
// macOS 上即 ~/Library/Application Support/mechbox/library.db;
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
	lib  *library.Library

	configMu sync.Mutex
	token    bool
	sync     *library.SyncSettings

	background chan struct{}
}

// NewLibraryService 创建绑定服务;库在 Wails 启动服务时才打开。
func NewLibraryService(path string) *LibraryService {
	return &LibraryService{path: path, background: make(chan struct{}, 1)}
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

func (s *LibraryService) inBackground(ctx context.Context, work func()) {
	select {
	case s.background <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() {
		debug.FreeOSMemory()
		<-s.background
	}()
	work()
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
	Version     string `json:"version"`      // 应用版本
}

// Info 返回技能库路径、技能库所用的用户主目录与应用版本。
func (s *LibraryService) Info() (AppInfo, error) {
	return AppInfo{LibraryPath: s.path, HomeDir: s.lib.HomeDirectory(), Version: appVersion()}, nil
}

//go:embed build/config.yml
var buildConfig string

func appVersion() string {
	inInfo := false
	scanner := bufio.NewScanner(strings.NewReader(buildConfig))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, " ") {
			inInfo = strings.HasPrefix(line, "info:")
			continue
		}
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "version:"); ok && inInfo {
			value, _, _ = strings.Cut(value, "#")
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// ConfigStatus 报告技能库依赖的外部配置是否就绪：设置页里填了就看设置页的，没填才看环境变量。
type ConfigStatus struct {
	Sync        bool `json:"sync"`         // S3 同步：存储桶与密钥齐全
	GitHubToken bool `json:"github_token"` // GitHub 令牌：设置页或 GITHUB_TOKEN / GH_TOKEN(都没有时匿名访问，易被限流)
}

// Config 返回外部配置的就绪状态，只看是否填了，不校验取值。
func (s *LibraryService) Config() ConfigStatus {
	set := func(names ...string) bool {
		for _, name := range names {
			if os.Getenv(name) == "" {
				return false
			}
		}
		return true
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	status := ConfigStatus{
		Sync:        set("SKILLS_S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"),
		GitHubToken: s.token || set("GITHUB_TOKEN") || set("GH_TOKEN"),
	}
	if s.sync != nil {
		status.Sync = s.sync.Bucket != "" && s.sync.AccessKeyID != "" && s.sync.SecretAccessKey != ""
	}
	return status
}
