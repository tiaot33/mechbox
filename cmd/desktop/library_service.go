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

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/mechbox/internal/library"
)

// appDirName 是技能库在用户配置目录下的子目录名。
const appDirName = "mechbox"

// libraryPathEnv 是本地调试用的逃生通道：设了就打开这个数据库，不打开真实技能库。
// 设置都存在数据库里，换一个数据库就是换一整套设置，不会和真实技能库的设置混在一起。
const libraryPathEnv = "MECHBOX_LIBRARY"

// defaultLibraryPath 返回技能库的位置：设了 MECHBOX_LIBRARY 时是它(相对路径按当前目录);
// 否则是真实技能库 <用户配置目录>/mechbox/library.db,macOS 上即 ~/Library/Application Support/mechbox/library.db,
// 从 Finder / Dock 启动时也可用，因为它只依赖 HOME 而不依赖 shell 环境变量。
func defaultLibraryPath() (string, error) {
	if path := os.Getenv(libraryPathEnv); path != "" {
		return filepath.Abs(path)
	}
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

	afterPull func(context.Context)

	background chan struct{}
}

// NewLibraryService 创建绑定服务;库在 Wails 启动服务时才打开。
func NewLibraryService(path string) *LibraryService {
	return &LibraryService{path: path, background: make(chan struct{}, 1)}
}

// ServiceStartup 在 Wails 启动服务时打开技能库：目录不存在则创建，库文件不存在则由 Open 初始化空库。
func (s *LibraryService) ServiceStartup(context.Context, application.ServiceOptions) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
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

// ConfigStatus 报告技能库依赖的配置是否就绪;配置都存在数据库里，不读环境变量。
type ConfigStatus struct {
	Sync        bool `json:"sync"`         // S3 同步：存储桶与密钥齐全
	GitHubToken bool `json:"github_token"` // GitHub 令牌：设置页填了(没填时匿名访问，易被限流)
}

// Config 返回配置的就绪状态，只看是否填了，不校验取值。
func (s *LibraryService) Config(ctx context.Context) (ConfigStatus, error) {
	token, err := s.lib.GitHubToken(ctx)
	if err != nil {
		return settle(ConfigStatus{}, err)(ctx)
	}
	sync, err := s.lib.SyncSettings(ctx)
	if err != nil {
		return settle(ConfigStatus{}, err)(ctx)
	}
	return ConfigStatus{
		Sync:        sync.Bucket != "" && sync.AccessKeyID != "" && sync.SecretAccessKey != "",
		GitHubToken: token != "",
	}, nil
}
