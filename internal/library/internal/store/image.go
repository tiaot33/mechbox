package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/mattn/go-sqlite3"
)

type Image struct {
	db   *sql.DB
	path string
}

type ProjectPathConflictError struct {
	Pending []string
}

func (e *ProjectPathConflictError) Error() string {
	return fmt.Sprintf("project path mapping blocked: %v", e.Pending)
}

func (s *Store) Snapshot(ctx context.Context) ([]byte, error) {
	snapshot, err := os.CreateTemp("", ".library-sync-*.db")
	if err != nil {
		return nil, fmt.Errorf("create sync snapshot: %w", err)
	}
	snapshotPath := snapshot.Name()
	_ = snapshot.Close()
	defer os.Remove(snapshotPath)
	snapshotDB, err := openDB(ctx, snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("open sync snapshot: %w", err)
	}
	if err := backup(ctx, snapshotDB, s.db); err != nil {
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

func OpenImage(ctx context.Context, data []byte) (*Image, error) {
	file, err := os.CreateTemp("", ".library-pull-*.db")
	if err != nil {
		return nil, fmt.Errorf("create pull file: %w", err)
	}
	image := &Image{path: file.Name()}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		image.Close()
		return nil, fmt.Errorf("write pulled library: %w", err)
	}
	if err := file.Close(); err != nil {
		image.Close()
		return nil, fmt.Errorf("close pulled library: %w", err)
	}
	image.db, err = openDB(ctx, image.path)
	if err == nil {
		err = validateImage(ctx, image.db)
	}
	if err != nil {
		image.Close()
		if sqliteErr, ok := errors.AsType[sqlite3.Error](err); ok && (sqliteErr.Code == sqlite3.ErrCorrupt || sqliteErr.Code == sqlite3.ErrNotADB) {
			err = fmt.Errorf("%w: %w", ErrUnacceptable, err)
		}
		return nil, fmt.Errorf("validate pulled library: %w", err)
	}
	return image, nil
}

func validateImage(ctx context.Context, db *sql.DB) error {
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("%w: integrity=%q", ErrUnacceptable, integrity)
	}
	if err := validateSchema(ctx, db); err != nil {
		return fmt.Errorf("validate pulled library: %w", err)
	}
	return nil
}

func (i *Image) Close() {
	if i.db != nil {
		_ = i.db.Close()
	}
	_ = os.Remove(i.path)
}

func (i *Image) RemapProjectPaths(ctx context.Context, paths map[string]string) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current := map[int64]string{}
	pending := map[int64]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id, path FROM projects`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
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
	var order []int64
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
			return &ProjectPathConflictError{Pending: stuck}
		}
	}
	for _, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET path = ? WHERE id = ?`, paths[current[id]], id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Replace(ctx context.Context, image *Image) error {
	return backup(ctx, s.db, image.db)
}

func backup(ctx context.Context, destination, source *sql.DB) error {
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
