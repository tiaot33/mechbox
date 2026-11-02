package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/store/migrations"
)

func upgrade(ctx context.Context, db *sql.DB, steps []migrations.Step) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return wrap(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return wrap(err)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`)
	for _, step := range steps {
		if err := upgradeStep(ctx, conn, step); err != nil {
			err = wrap(err)
			if !errors.Is(err, ErrBusy) {
				err = fmt.Errorf("%w: %w", ErrUnacceptable, err)
			}
			return fmt.Errorf("upgrade library to schema version %d (%s): %w", step.Version, step.Name, err)
		}
	}
	return nil
}

func upgradeStep(ctx context.Context, conn *sql.Conn, step migrations.Step) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= step.Version {
		return nil
	}
	if version != step.Version-1 {
		return fmt.Errorf("library is at schema version %d", version)
	}
	if step.Go != nil {
		err = step.Go(ctx, tx)
	} else {
		_, err = tx.ExecContext(ctx, step.SQL)
	}
	if err != nil {
		return err
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
		return errors.New("foreign key check failed")
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, step.Version)); err != nil {
		return err
	}
	return tx.Commit()
}

func backupBeforeUpgrade(ctx context.Context, db *sql.DB, path string, version int) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	ext := filepath.Ext(path)
	target := fmt.Sprintf("%s-v%d%s", strings.TrimSuffix(path, ext), version, ext)
	temp := target + ".partial"
	_ = os.Remove(temp)
	if err := restrictToOwner(temp); err != nil {
		return err
	}
	err := copyDatabase(ctx, db, temp)
	if err == nil {
		err = os.Rename(temp, target)
	}
	if err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func copyDatabase(ctx context.Context, source *sql.DB, path string) error {
	destination, err := openDB(ctx, path)
	if err != nil {
		return err
	}
	if err := backup(ctx, destination, source); err != nil {
		_ = destination.Close()
		return err
	}
	return destination.Close()
}
