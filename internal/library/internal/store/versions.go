package store

import (
	"context"
	"database/sql"
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
}

type FileInfo struct {
	Path string
	Size int64
	Mode skillfile.Mode
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
	if len(versions) == 0 {
		return nil, fmt.Errorf("list skill versions: %w", ErrNotFound)
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
	return version, nil
}

func (q queries) ListVersionFiles(ctx context.Context, versionID int64) ([]FileInfo, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT version_files.path, length(blobs.content), version_files.mode
		FROM skill_versions
		LEFT JOIN version_files ON version_files.version_id = skill_versions.id
		LEFT JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE skill_versions.id = ?
		ORDER BY version_files.path
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("list version files: %w", wrap(err))
	}
	defer rows.Close()
	versionExists := false
	var files []FileInfo
	for rows.Next() {
		versionExists = true
		var path, mode sql.NullString
		var size sql.NullInt64
		if err := rows.Scan(&path, &size, &mode); err != nil {
			return nil, fmt.Errorf("read version file metadata: %w", wrap(err))
		}
		if path.Valid {
			files = append(files, FileInfo{Path: path.String, Size: size.Int64, Mode: skillfile.Mode(mode.String)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list version files: %w", wrap(err))
	}
	if !versionExists {
		return nil, fmt.Errorf("list version files: %w", ErrNotFound)
	}
	return files, nil
}

func (q queries) GetVersionFile(ctx context.Context, versionID int64, path string) (skillfile.File, bool, error) {
	var fileExists bool
	var mode sql.NullString
	file := skillfile.File{Path: path}
	err := q.q.QueryRowContext(ctx, `
		SELECT version_files.path IS NOT NULL, blobs.content, version_files.mode
		FROM skill_versions
		LEFT JOIN version_files ON version_files.version_id = skill_versions.id AND version_files.path = ?
		LEFT JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE skill_versions.id = ?
	`, path, versionID).Scan(&fileExists, &file.Content, &mode)
	if err != nil {
		return skillfile.File{}, false, fmt.Errorf("get version file: %w", wrap(err))
	}
	if !fileExists {
		return skillfile.File{}, false, nil
	}
	file.Mode = skillfile.Mode(mode.String)
	return file, true, nil
}

func (q queries) VersionDigests(ctx context.Context, versionID int64) (map[string]skillfile.Digest, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT version_files.path, version_files.fingerprint, version_files.mode
		FROM skill_versions
		LEFT JOIN version_files ON version_files.version_id = skill_versions.id
		WHERE skill_versions.id = ?
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("list version fingerprints: %w", wrap(err))
	}
	defer rows.Close()
	versionExists := false
	files := make(map[string]skillfile.Digest)
	for rows.Next() {
		versionExists = true
		var path, fingerprint, mode sql.NullString
		if err := rows.Scan(&path, &fingerprint, &mode); err != nil {
			return nil, fmt.Errorf("read version fingerprint: %w", wrap(err))
		}
		if path.Valid {
			files[path.String] = skillfile.Digest{Fingerprint: fingerprint.String, Mode: skillfile.Mode(mode.String)}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read version fingerprints: %w", wrap(err))
	}
	if !versionExists {
		return nil, fmt.Errorf("list version fingerprints: %w", ErrNotFound)
	}
	return files, nil
}

func (q queries) VersionFiles(ctx context.Context, versionID int64) ([]skillfile.File, error) {
	return q.versionFiles(ctx, versionID, "get version files")
}
