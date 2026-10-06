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

const pullFolderWarning = "agent 目录里已安装的技能文件夹未随本次 pull 重建,可能与新库不一致"

func (l *sqliteLibrary) PlanSyncPush(ctx context.Context) (plan SyncPlan, err error) {
	defer func() { err = typedError(err) }()
	identity, exists, err := remoteIdentity(ctx)
	if err != nil {
		return SyncPlan{}, err
	}
	return SyncPlan{Remote: identity, RemoteExists: exists}, nil
}

func (l *sqliteLibrary) PlanSyncPull(ctx context.Context) (plan SyncPlan, err error) {
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

func (l *sqliteLibrary) SyncPush(ctx context.Context, expected RemoteIdentity) (result SyncResult, err error) {
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

func (l *sqliteLibrary) SyncPull(ctx context.Context, expected RemoteIdentity) (result SyncResult, err error) {
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

func (l *sqliteLibrary) snapshotBytes(ctx context.Context) ([]byte, error) {
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
