package store

import (
	"context"
	"fmt"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

type Version struct {
	ID          int64
	SkillID     int64
	Description string
	Revision    string
	CreatedAt   time.Time
	Files       []skillfile.File
}

func (t *Tx) AddVersion(ctx context.Context, skillID int64, version NewVersion) (int64, error) {
	result, err := t.q.ExecContext(ctx, `INSERT INTO skill_versions(skill_id, source_state, revision, description, created_at) VALUES (?, ?, ?, ?, ?)`, skillID, version.State, version.Revision, version.Description, formatTime(version.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("store skill version: %w", wrap(err))
	}
	versionID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read version id: %w", wrap(err))
	}
	for _, file := range version.Files {
		fingerprint := skillfile.Fingerprint(file.Content)
		if _, err := t.q.ExecContext(ctx, `INSERT OR IGNORE INTO blobs(fingerprint, content) VALUES (?, ?)`, fingerprint, file.Content); err != nil {
			return 0, fmt.Errorf("store %s: %w", file.Path, wrap(err))
		}
		if _, err := t.q.ExecContext(ctx, `INSERT INTO version_files(version_id, path, fingerprint, mode) VALUES (?, ?, ?, ?)`, versionID, file.Path, fingerprint, file.Mode); err != nil {
			return 0, fmt.Errorf("store %s metadata: %w", file.Path, wrap(err))
		}
	}
	if _, err := t.q.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, skillID); err != nil {
		return 0, fmt.Errorf("select current version: %w", wrap(err))
	}
	return versionID, nil
}

func (q queries) ListVersions(ctx context.Context, skillID int64) ([]Version, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id, description, revision, created_at FROM skill_versions WHERE skill_id = ? ORDER BY id`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", wrap(err))
	}
	defer rows.Close()
	var versions []Version
	for rows.Next() {
		var createdAt string
		version := Version{SkillID: skillID}
		if err := rows.Scan(&version.ID, &version.Description, &version.Revision, &createdAt); err != nil {
			return nil, fmt.Errorf("read skill version: %w", wrap(err))
		}
		if version.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, fmt.Errorf("read skill version: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read skill versions: %w", wrap(err))
	}
	return versions, nil
}

func (q queries) GetVersion(ctx context.Context, id int64) (Version, error) {
	version := Version{ID: id}
	var createdAt string
	if err := q.q.QueryRowContext(ctx, `SELECT skill_id, description, revision, created_at FROM skill_versions WHERE id = ?`, id).Scan(&version.SkillID, &version.Description, &version.Revision, &createdAt); err != nil {
		return Version{}, fmt.Errorf("get skill version: %w", wrap(err))
	}
	var err error
	if version.CreatedAt, err = parseTime(createdAt); err != nil {
		return Version{}, fmt.Errorf("get skill version: %w", err)
	}
	if version.Files, err = q.versionFiles(ctx, id, "get skill version files"); err != nil {
		return Version{}, err
	}
	return version, nil
}
