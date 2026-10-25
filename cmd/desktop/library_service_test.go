package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/mechbox/internal/library"
)

func TestLibraryServiceCreatesLibraryInMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", appDirName, "library.db")
	service := NewLibraryService(path)
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() {
		if err := service.ServiceShutdown(); err != nil {
			t.Errorf("ServiceShutdown: %v", err)
		}
	})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("library file not created: %v", err)
	}
	skills, err := service.ListSkills(t.Context())
	if err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills = %v, %v; want empty", skills, err)
	}
	collections, err := service.ListCollections(t.Context())
	if err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections = %v, %v; want empty", collections, err)
	}
	groups, err := service.ListGroups(t.Context())
	if err != nil || len(groups) != 0 {
		t.Fatalf("ListGroups = %v, %v; want empty", groups, err)
	}
	skillScopes, err := service.InspectSkillScopes(t.Context(), library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("InspectSkillScopes = %v, %v; want empty", skillScopes, err)
	}
	agents, err := service.ListAgents(t.Context())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ListAgents = %v, %v; want built-in agents", agents, err)
	}
	info, err := service.Info()
	if err != nil || info.LibraryPath != path || info.HomeDir == "" {
		t.Fatalf("Info = %+v, %v; want library path %q and a home directory", info, err, path)
	}
}

func TestLibraryServiceRejectsForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := NewLibraryService(path)
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err == nil {
		_ = service.ServiceShutdown()
		t.Fatal("ServiceStartup succeeded on a foreign file")
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatalf("ServiceShutdown after failed startup: %v", err)
	}
}

func TestLibraryServiceSwallowsErrorsOfCancelledCalls(t *testing.T) {
	service := NewLibraryService(filepath.Join(t.TempDir(), "library.db"))
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = service.ServiceShutdown() })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ListSkills(ctx); err != nil {
		t.Fatalf("ListSkills on a cancelled call = %v; want nil so Wails discards the result", err)
	}
	// 未取消的调用照常返回错误。
	if _, err := service.ScopeDirectory(t.Context(), library.Scope{Kind: "bogus"}); err == nil {
		t.Fatal("ScopeDirectory with an invalid scope succeeded")
	}
}

// 找来源要等进行中的自动检查更新做完才开始;等的时候前端取消了，就不再找。
func TestFindUnmanagedSourcesWaitsForBackgroundWork(t *testing.T) {
	service := NewLibraryService(filepath.Join(t.TempDir(), "library.db"))
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = service.ServiceShutdown() })
	req := library.FindUnmanagedSourcesRequest{Folders: []library.FolderInScope{{Scope: library.Scope{Kind: "bogus"}, Name: "pdf"}}}

	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		service.inBackground(t.Context(), func() {
			close(started)
			<-release
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if found, err := service.FindUnmanagedSources(ctx, req); found != nil || err != nil {
		t.Fatalf("FindUnmanagedSources during background work = %v, %v; want it to wait until cancelled", found, err)
	}
	close(release)
	<-done
	if _, err := service.FindUnmanagedSources(t.Context(), req); err == nil {
		t.Fatal("FindUnmanagedSources with an invalid scope succeeded after background work finished")
	}
}

func TestMarshalErrorEncodesLibraryError(t *testing.T) {
	var got library.Error
	if err := json.Unmarshal(marshalError(errors.New("boom")), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != library.ErrorOf(errors.New("boom")).Code || got.Message == "" {
		t.Fatalf("marshalError = %+v; want code and message from library.ErrorOf", got)
	}
}

// MECHBOX_LIBRARY 设了就打开它(相对路径按当前目录),不打开真实技能库;没设时是用户配置目录下的 mechbox/library.db。
func TestLibraryPathCanPointAtAnotherDatabase(t *testing.T) {
	t.Setenv(libraryPathEnv, "")
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := defaultLibraryPath(); err != nil || got != filepath.Join(config, appDirName, "library.db") {
		t.Fatalf("defaultLibraryPath() = %q, %v; want the real library", got, err)
	}
	dev := filepath.Join(t.TempDir(), "dev.db")
	t.Setenv(libraryPathEnv, dev)
	if got, err := defaultLibraryPath(); err != nil || got != dev {
		t.Fatalf("defaultLibraryPath() with %s = %q, %v; want %q", libraryPathEnv, got, err, dev)
	}
	t.Setenv(libraryPathEnv, "dev.db")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := defaultLibraryPath(); err != nil || got != filepath.Join(wd, "dev.db") {
		t.Fatalf("defaultLibraryPath() with a relative path = %q, %v", got, err)
	}
}

// 配置状态只看数据库里存的,环境变量里的令牌与 S3 设置不算。
func TestConfigStatusOnlyCountsStoredSettings(t *testing.T) {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "SKILLS_S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Setenv(name, "from-environment")
	}
	service := startLibrary(t)
	if got, err := service.Config(t.Context()); err != nil || got != (ConfigStatus{}) {
		t.Fatalf("Config with only environment variables = %+v, %v; want nothing configured", got, err)
	}
	if err := service.SetGitHubToken(t.Context(), "stored"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSyncSettings(t.Context(), library.SyncSettings{Bucket: "b", AccessKeyID: "AKID"}); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Config(t.Context()); err != nil || got != (ConfigStatus{GitHubToken: true}) {
		t.Fatalf("Config without a secret key = %+v, %v", got, err)
	}
	if err := service.SetSyncSettings(t.Context(), library.SyncSettings{Bucket: "b", AccessKeyID: "AKID", SecretAccessKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Config(t.Context()); err != nil || got != (ConfigStatus{Sync: true, GitHubToken: true}) {
		t.Fatalf("Config with stored settings = %+v, %v", got, err)
	}
}
