package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

type versionRecord struct {
	treeSHA         string
	confirmedCommit string
	state           source.State
}

func sourceVersionRecord(src Source, treeSHA string, state source.State) versionRecord {
	record := versionRecord{treeSHA: treeSHA, state: state}
	if src.Type == SourceGitHub {
		record.confirmedCommit = src.LastSeenRef
	}
	return record
}

func createVersion(ctx context.Context, tx *sql.Tx, skillID int64, files []File, record versionRecord, description string, createdAt time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO skill_versions(skill_id, git_tree_sha, confirmed_commit, source_state, description, created_at) VALUES (?, ?, ?, ?, ?, ?)`, skillID, record.treeSHA, record.confirmedCommit, string(record.state), description, formatStoredTime(createdAt))
	if err != nil {
		return 0, fmt.Errorf("store skill version: %w", err)
	}
	versionID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read version id: %w", err)
	}
	for _, file := range files {
		fingerprint := skillfile.Fingerprint(file.Content)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blobs(fingerprint, content) VALUES (?, ?)`, fingerprint, file.Content); err != nil {
			return 0, fmt.Errorf("store %s: %w", file.Path, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO version_files(version_id, path, fingerprint, mode) VALUES (?, ?, ?, ?)`, versionID, file.Path, fingerprint, file.Mode); err != nil {
			return 0, fmt.Errorf("store %s metadata: %w", file.Path, err)
		}
	}
	return versionID, nil
}

func (l *sqliteLibrary) ListVersions(ctx context.Context, id SkillID) (versions []SkillVersion, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, git_tree_sha, description, created_at FROM skill_versions WHERE skill_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var createdAt string
		version := SkillVersion{SkillID: id}
		if err := rows.Scan(&version.ID, &version.GitTreeSHA, &version.Description, &createdAt); err != nil {
			return nil, fmt.Errorf("read skill version: %w", err)
		}
		if version.CreatedAt, err = parseStoredTime(createdAt); err != nil {
			return nil, fmt.Errorf("read skill version: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read skill versions: %w", err)
	}
	return versions, nil
}

func (l *sqliteLibrary) GetVersion(ctx context.Context, id VersionID) (version SkillVersion, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	version = SkillVersion{ID: id}
	var createdAt string
	if err := l.db.QueryRowContext(ctx, `SELECT skill_id, git_tree_sha, description, created_at FROM skill_versions WHERE id = ?`, id).Scan(&version.SkillID, &version.GitTreeSHA, &version.Description, &createdAt); err != nil {
		return SkillVersion{}, fmt.Errorf("get skill version: %w", err)
	}
	if version.CreatedAt, err = parseStoredTime(createdAt); err != nil {
		return SkillVersion{}, fmt.Errorf("get skill version: %w", err)
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT version_files.path, blobs.content, version_files.mode
		FROM version_files JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE version_files.version_id = ?
		ORDER BY version_files.path
	`, id)
	if err != nil {
		return SkillVersion{}, fmt.Errorf("get skill version files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.Path, &file.Content, &file.Mode); err != nil {
			return SkillVersion{}, fmt.Errorf("read skill version file: %w", err)
		}
		version.Files = append(version.Files, file)
	}
	if err := rows.Err(); err != nil {
		return SkillVersion{}, fmt.Errorf("read skill version files: %w", err)
	}
	return version, nil
}

func (l *sqliteLibrary) DiffVersions(ctx context.Context, oldID, newID VersionID) (diff VersionDiff, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	oldVersion, err := l.GetVersion(ctx, oldID)
	if err != nil {
		return VersionDiff{}, err
	}
	newVersion, err := l.GetVersion(ctx, newID)
	if err != nil {
		return VersionDiff{}, err
	}
	if oldVersion.SkillID != newVersion.SkillID {
		return VersionDiff{}, argumentError("diff skill versions", "", "versions belong to different skills")
	}
	oldFiles := make(map[string]string, len(oldVersion.Files))
	for _, file := range oldVersion.Files {
		oldFiles[file.Path] = skillfile.Fingerprint(file.Content)
	}
	newFiles := make(map[string]string, len(newVersion.Files))
	for _, file := range newVersion.Files {
		newFiles[file.Path] = skillfile.Fingerprint(file.Content)
	}
	for _, file := range oldVersion.Files {
		newSum, ok := newFiles[file.Path]
		if !ok {
			diff.Deleted = append(diff.Deleted, file.Path)
		} else if oldFiles[file.Path] != newSum {
			diff.Changed = append(diff.Changed, file.Path)
		}
	}
	for _, file := range newVersion.Files {
		if _, ok := oldFiles[file.Path]; !ok {
			diff.Added = append(diff.Added, file.Path)
		}
	}
	return diff, nil
}
