package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/s3"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// RemoteIdentity 是 Sync 远端对象的身份标识(S3 ETag);空值表示远端当前无对象。
// 同步计划时返回,执行时回传以绑定"用户确认的"与"实际推/拉的"是同一个远端对象。
type RemoteIdentity string

// SyncPlan 是 Sync 推/拉执行前的只读报告:远端身份 + 相关校验提示,不改动本地库,
// 也不提交推送。调用方据此确认后再带着 Remote 调用对应的执行方法。
type SyncPlan struct {
	Remote       RemoteIdentity `json:"remote"`        // 当前远端身份;RemoteExists 为 false 时无意义
	RemoteExists bool           `json:"remote_exists"` // 远端是否已存在对象
	SchemaOK     bool           `json:"schema_ok"`     // 仅 pull:远端库的 schema 是否可接受
	Warnings     []string       `json:"warnings,omitempty"`
}

// SyncResult 是 Sync 推/拉成功后的结果:操作后的远端身份 + 可能的警告。
// 警告表示操作已成功但调用方需留意某事(如 pull 后磁盘上已安装的文件夹可能与新库不一致)。
type SyncResult struct {
	Remote   RemoteIdentity `json:"remote"`
	Warnings []string       `json:"warnings,omitempty"`
}

const pullFolderWarning = "agent 目录里已安装的技能文件夹未随本次 pull 重建,可能与新库不一致"

// PlanSyncPush 生成推送的同步计划:报告推送目标的当前远端身份(若有),以便调用方知道可能覆盖远端。
func (l *Library) PlanSyncPush(ctx context.Context) (plan SyncPlan, err error) {
	defer func() { err = typedError(err) }()
	identity, exists, err := remoteIdentity(ctx)
	if err != nil {
		return SyncPlan{}, err
	}
	return SyncPlan{Remote: identity, RemoteExists: exists}, nil
}

// PlanSyncPull 生成拉取的同步计划:报告远端库的身份与 schema 是否可接受,不替换本地库。
func (l *Library) PlanSyncPull(ctx context.Context) (plan SyncPlan, err error) {
	defer func() { err = typedError(err) }()
	data, etag, err := s3.Get(ctx)
	if err != nil {
		return SyncPlan{}, err
	}
	image, err := store.OpenImage(ctx, data)
	if err == nil {
		defer image.Close()
	} else if !errors.Is(err, store.ErrUnacceptable) {
		return SyncPlan{}, err
	}
	schemaOK := err == nil
	plan = SyncPlan{Remote: RemoteIdentity(etag), RemoteExists: true, SchemaOK: schemaOK}
	if !schemaOK {
		plan.Warnings = append(plan.Warnings, "远端库的 schema 不可接受,pull 将被拒绝")
	}
	plan.Warnings = append(plan.Warnings, pullFolderWarning)
	return plan, nil
}

// SyncPush 把整个数据库推送到 S3;expected 为同步计划里的远端身份,若远端已变则返回
// Conflict,调用方须重新生成同步计划。
func (l *Library) SyncPush(ctx context.Context, expected RemoteIdentity) (result SyncResult, err error) {
	defer func() { err = typedError(err) }()
	identity, _, err := remoteIdentity(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	if identity != expected {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync push: remote identity changed since sync plan"}
	}
	data, err := l.snapshotBytes(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	etag, err := s3.Put(ctx, data, string(expected))
	if err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Remote: RemoteIdentity(etag)}, nil
}

// SyncPull 从 S3 拉回整库并原子替换本地,替换前校验 schema 并重映射项目路径;
// expected 为同步计划里的远端身份,若远端已变则返回 Conflict。
// 不重建或清理磁盘上已安装的文件夹,结果可能带警告提示它们可能与新库不一致。
func (l *Library) SyncPull(ctx context.Context, expected RemoteIdentity) (result SyncResult, err error) {
	defer func() { err = typedError(err) }()
	data, etag, err := s3.Get(ctx)
	if errors.Is(err, s3.ErrNotFound) && expected != "" {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync pull: remote object deleted since sync plan"}
	}
	if err != nil {
		return SyncResult{}, err
	}
	if RemoteIdentity(etag) != expected {
		return SyncResult{}, &Error{Code: Conflict, Message: "sync pull: remote identity changed since sync plan"}
	}
	image, err := store.OpenImage(ctx, data)
	if err != nil {
		if errors.Is(err, store.ErrUnacceptable) {
			failure := preconditionError("sync pull", "remote library is not acceptable", ErrorDetails{})
			failure.Err = err
			return SyncResult{}, failure
		}
		return SyncResult{}, err
	}
	defer image.Close()
	if err := remapProjectPaths(ctx, image, os.Getenv("SKILLS_PROJECT_PATH_MAP")); err != nil {
		return SyncResult{}, fmt.Errorf("remap pulled projects: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	home, err := os.UserHomeDir()
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync pull: find home directory: %w", err)
	}
	l.home = home
	if err := l.store.Replace(ctx, image); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Remote: RemoteIdentity(etag), Warnings: []string{pullFolderWarning}}, nil
}

func remoteIdentity(ctx context.Context) (RemoteIdentity, bool, error) {
	etag, err := s3.Head(ctx)
	if err != nil {
		if errors.Is(err, s3.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return RemoteIdentity(etag), true, nil
}

func (l *Library) snapshotBytes(ctx context.Context) ([]byte, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.store.Snapshot(ctx)
}

func remapProjectPaths(ctx context.Context, image *store.Image, raw string) error {
	paths := map[string]string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &paths); err != nil {
			return &Error{Code: InvalidArgument, Message: "decode SKILLS_PROJECT_PATH_MAP: " + err.Error(), Err: err}
		}
	}
	for old, newPath := range paths {
		if old == "" || newPath == "" {
			return &Error{Code: InvalidArgument, Message: "SKILLS_PROJECT_PATH_MAP: project path mapping cannot be empty"}
		}
		absolute, err := filepath.Abs(newPath)
		if err != nil {
			return err
		}
		paths[old] = filepath.Clean(absolute)
	}
	return image.RemapProjectPaths(ctx, paths)
}
