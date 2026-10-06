package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/tiaot33/skills-tui/internal/library/internal/s3"
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
	_, cleanup, err := openPulledLibrary(ctx, data)
	if err == nil {
		defer cleanup()
	} else if !unacceptableRemote(err) {
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
	check, cleanup, err := openPulledLibrary(ctx, data)
	if err != nil {
		if unacceptableRemote(err) {
			failure := preconditionError("sync pull", "remote library is not acceptable", ErrorDetails{})
			failure.Err = err
			return SyncResult{}, failure
		}
		return SyncResult{}, err
	}
	defer cleanup()
	if err := remapProjectPaths(ctx, check, os.Getenv("SKILLS_PROJECT_PATH_MAP")); err != nil {
		return SyncResult{}, fmt.Errorf("remap pulled projects: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	home, err := os.UserHomeDir()
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync pull: find home directory: %w", err)
	}
	l.home = home
	if err := backupDatabase(ctx, l.db, check); err != nil {
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
	snapshot, err := os.CreateTemp("", ".library-sync-*.db")
	if err != nil {
		return nil, fmt.Errorf("create sync snapshot: %w", err)
	}
	snapshotPath := snapshot.Name()
	_ = snapshot.Close()
	defer os.Remove(snapshotPath)
	snapshotDB, err := openLibraryDB(ctx, snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("open sync snapshot: %w", err)
	}
	if err := backupDatabase(ctx, snapshotDB, l.db); err != nil {
		_ = snapshotDB.Close()
		return nil, fmt.Errorf("snapshot library: %w", err)
	}
	var mode string
	err = snapshotDB.QueryRowContext(ctx, `PRAGMA journal_mode = DELETE`).Scan(&mode)
	if err == nil && mode != "delete" {
		err = fmt.Errorf("journal_mode stayed %q", mode)
	}
	if err != nil {
		_ = snapshotDB.Close()
		return nil, fmt.Errorf("reset sync snapshot journal mode: %w", err)
	}
	if err := snapshotDB.Close(); err != nil {
		return nil, fmt.Errorf("close sync snapshot: %w", err)
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("read sync snapshot: %w", err)
	}
	return data, nil
}

func openPulledLibrary(ctx context.Context, data []byte) (*sql.DB, func(), error) {
	download, err := os.CreateTemp("", ".library-pull-*.db")
	if err != nil {
		return nil, nil, fmt.Errorf("create pull file: %w", err)
	}
	downloadPath := download.Name()
	var check *sql.DB
	cleanup := func() {
		if check != nil {
			_ = check.Close()
		}
		_ = os.Remove(downloadPath)
	}
	if _, err := download.Write(data); err != nil {
		_ = download.Close()
		cleanup()
		return nil, nil, fmt.Errorf("write pulled library: %w", err)
	}
	if err := download.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("close pulled library: %w", err)
	}
	check, err = openLibraryDB(ctx, downloadPath)
	if err == nil {
		err = validatePulledDatabase(ctx, check)
	}
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("validate pulled library: %w", err)
	}
	return check, cleanup, nil
}

func unacceptableRemote(err error) bool {
	if errors.Is(err, errUnacceptableSchema) {
		return true
	}
	sqliteErr, ok := errors.AsType[sqlite3.Error](err)
	return ok && (sqliteErr.Code == sqlite3.ErrCorrupt || sqliteErr.Code == sqlite3.ErrNotADB)
}

func validatePulledDatabase(ctx context.Context, check *sql.DB) error {
	var integrity string
	if err := check.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("%w: integrity=%q", errUnacceptableSchema, integrity)
	}
	if err := validateLibrarySchema(ctx, check); err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	return nil
}

func backupDatabase(ctx context.Context, destination, source *sql.DB) error {
	destinationConn, err := destination.Conn(ctx)
	if err != nil {
		return err
	}
	defer destinationConn.Close()
	sourceConn, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer sourceConn.Close()
	return destinationConn.Raw(func(destinationDriver any) error {
		destinationSQLite, ok := destinationDriver.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("unexpected destination SQLite driver %T", destinationDriver)
		}
		return sourceConn.Raw(func(sourceDriver any) error {
			sourceSQLite, ok := sourceDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("unexpected source SQLite driver %T", sourceDriver)
			}
			backup, err := destinationSQLite.Backup("main", sourceSQLite, "main")
			if err != nil {
				return err
			}
			for {
				done, err := backup.Step(128)
				if err != nil {
					_ = backup.Close()
					return err
				}
				if done {
					return backup.Close()
				}
				select {
				case <-ctx.Done():
					_ = backup.Close()
					return ctx.Err()
				case <-time.After(time.Millisecond):
				}
			}
		})
	})
}

func remapProjectPaths(ctx context.Context, db *sql.DB, raw string) error {
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
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current := map[ProjectID]string{}
	pending := map[ProjectID]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id, path FROM projects`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id ProjectID
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			_ = rows.Close()
			return err
		}
		current[id] = path
		if newPath, ok := paths[path]; ok && newPath != path {
			pending[id] = newPath
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	occupied := map[string]bool{}
	for _, path := range current {
		occupied[path] = true
	}
	var order []ProjectID
	for len(pending) > 0 {
		progressed := false
		for _, id := range slices.Sorted(maps.Keys(pending)) {
			if occupied[pending[id]] {
				continue
			}
			delete(occupied, current[id])
			occupied[pending[id]] = true
			order = append(order, id)
			delete(pending, id)
			progressed = true
		}
		if !progressed {
			var stuck []string
			for _, id := range slices.Sorted(maps.Keys(pending)) {
				stuck = append(stuck, current[id]+" -> "+pending[id])
			}
			return &Error{Code: InvalidArgument, Message: "SKILLS_PROJECT_PATH_MAP: target path still held by another registered project (occupied target or cyclic mapping): " + strings.Join(stuck, ", ")}
		}
	}
	for _, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET path = ? WHERE id = ?`, paths[current[id]], id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
