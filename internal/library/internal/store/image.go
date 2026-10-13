package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-sqlite3"
)

type Image struct {
	db   *sql.DB
	path string
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
		return nil, fmt.Errorf("snapshot library: %w", wrap(err))
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
		return nil, fmt.Errorf("validate pulled library: %w", wrap(err))
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

type Summary struct {
	Skills   int
	Groups   int
	Projects []Project
}

func (i *Image) Summary(ctx context.Context) (Summary, error) {
	var summary Summary
	if err := i.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM groups)`).Scan(&summary.Skills, &summary.Groups); err != nil {
		return Summary{}, wrap(err)
	}
	projects, err := queries{i.db}.ListProjects(ctx)
	if err != nil {
		return Summary{}, err
	}
	summary.Projects = projects
	return summary, nil
}

func (s *Store) Replace(ctx context.Context, image *Image) error {
	if err := writeBuiltinAgents(ctx, image.db); err != nil {
		return wrap(err)
	}
	if _, err := image.db.ExecContext(ctx, `UPDATE replica SET token = ?`, newToken()); err != nil {
		return wrap(err)
	}
	return wrap(backup(ctx, s.db, image.db))
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
