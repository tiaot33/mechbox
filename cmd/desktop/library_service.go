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

// settle 处理前端已取消的调用：前端 promise.cancel() 后 ctx 被取消，此时方法若返回错误,
// Wails 运行时会把这个"取消之后的拒绝"报成未处理的 rejection;返回正常结果则会被运行时丢弃。
// 所以调用已取消时吞掉错误、返回零值——前端早已不要这次结果。用法：return settle(s.lib.X(ctx))(ctx)。
func settle[T any](v T, err error) func(context.Context) (T, error) {
	return func(ctx context.Context) (T, error) {
		if err != nil && ctx.Err() != nil {
			var zero T
			return zero, nil
		}
		return v, err
	}
}

// 以下方法转发技能库的只读查询;每个都经 settle 处理取消。

// ListSkills 列出库中全部技能。
func (s *LibraryService) ListSkills(ctx context.Context) ([]library.SkillSummary, error) {
	return settle(s.lib.ListSkills(ctx))(ctx)
}

// ListCollections 列出按来源自动归集的集合。
func (s *LibraryService) ListCollections(ctx context.Context) ([]library.Collection, error) {
	return settle(s.lib.ListCollections(ctx))(ctx)
}

// ListGroupsWithMembers 列出全部组及其成员。
func (s *LibraryService) ListGroupsWithMembers(ctx context.Context) ([]library.GroupWithMembers, error) {
	return settle(s.lib.ListGroupsWithMembers(ctx))(ctx)
}

// ListAgents 列出技能库支持的 Agent。
func (s *LibraryService) ListAgents(ctx context.Context) ([]library.Agent, error) {
	return settle(s.lib.ListAgents(ctx))(ctx)
}

// ListRegisteredProjects 列出已登记项目。
func (s *LibraryService) ListRegisteredProjects(ctx context.Context) ([]library.RegisteredProject, error) {
	return settle(s.lib.ListRegisteredProjects(ctx))(ctx)
}

// ListGroupActivations 列出全部组激活。
func (s *LibraryService) ListGroupActivations(ctx context.Context) ([]library.GroupActivation, error) {
	return settle(s.lib.ListGroupActivations(ctx))(ctx)
}

// InspectProjections 对照磁盘给出投影及其状态;只读。
func (s *LibraryService) InspectProjections(ctx context.Context, filter library.ProjectionFilter) ([]library.ProjectionInspection, error) {
	return settle(s.lib.InspectProjections(ctx, filter))(ctx)
}

// Resolve 返回作用域按 Agent 现行布局算出的投影目录。
func (s *LibraryService) Resolve(ctx context.Context, scope library.Scope) (string, error) {
	return settle(s.lib.Resolve(ctx, scope))(ctx)
}
