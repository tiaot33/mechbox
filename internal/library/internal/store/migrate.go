package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/store/migrations"
)

func upgrade(ctx context.Context, db *sql.DB, steps []migrations.Step, validate func(context.Context, *sql.Tx) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return wrap(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return wrap(err)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`)
	if err := upgradeSteps(ctx, conn, steps, validate); err != nil {
		err = wrap(err)
		if !errors.Is(err, ErrBusy) {
			err = fmt.Errorf("%w: %w", ErrUnacceptable, err)
		}
		return fmt.Errorf("upgrade library: %w", err)
	}
	return nil
}

func upgradeSteps(ctx context.Context, conn *sql.Conn, steps []migrations.Step, validate func(context.Context, *sql.Tx) error) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&from); err != nil {
		return err
	}
	version := from
	for _, step := range steps {
		if step.Version <= version {
			continue
		}
		if step.Version != version+1 {
			return fmt.Errorf("library is at schema version %d, next migration is %d", version, step.Version)
		}
		if step.Go != nil {
			err = step.Go(ctx, tx)
		} else {
			_, err = tx.ExecContext(ctx, step.SQL)
		}
		if err != nil {
			return fmt.Errorf("schema version %d (%s): %w", step.Version, step.Name, err)
		}
		version = step.Version
	}
	if version == from {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	violated := rows.Next()
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if violated {
		return fmt.Errorf("foreign key check failed after upgrading from schema version %d to %d", from, version)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return err
	}
	if validate != nil {
		if err := validate(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func backupBeforeUpgrade(ctx context.Context, db *sql.DB, path string, version int) error {
	file, ok := databaseFile(path)
	if !ok {
		return nil
	}
	ext := filepath.Ext(file)
	target := fmt.Sprintf("%s-v%d%s", strings.TrimSuffix(file, ext), version, ext)
	temp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.partial")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Close(); err != nil {
		return err
	}
	copied, err := copyDatabase(ctx, db, tempPath)
	if err != nil || copied != version {
		return err
	}
	return os.Rename(tempPath, target)
}

func databaseFile(path string) (string, bool) {
	if path == "" || path == ":memory:" {
		return "", false
	}
	if !strings.HasPrefix(path, "file:") {
		return path, true
	}
	uri, err := url.Parse(path)
	if err != nil || uri.Query().Get("mode") == "memory" {
		return "", false
	}
	file := uri.Path
	if uri.Opaque != "" {
		file = uri.Opaque
	}
	if file == "" || file == ":memory:" {
		return "", false
	}
	if len(file) > 2 && file[0] == '/' && file[2] == ':' {
		file = file[1:]
	}
	return filepath.FromSlash(file), true
}

func copyDatabase(ctx context.Context, source *sql.DB, path string) (int, error) {
	destination, err := openDB(ctx, path)
	if err != nil {
		return 0, err
	}
	var version int
	var mode string
	err = backup(ctx, destination, source)
	if err == nil {
		err = destination.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
	}
	if err == nil {
		err = destination.QueryRowContext(ctx, `PRAGMA journal_mode = DELETE`).Scan(&mode)
	}
	if err == nil && mode != "delete" {
		err = fmt.Errorf("journal_mode stayed %q", mode)
	}
	return version, errors.Join(err, destination.Close())
}
