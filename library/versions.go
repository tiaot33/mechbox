package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

func createVersion(ctx context.Context, tx *sql.Tx, skillID int64, files []File, gitTreeSHA string) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO skill_versions(skill_id, git_tree_sha) VALUES (?, ?)`, skillID, gitTreeSHA)
	if err != nil {
		return 0, fmt.Errorf("store skill version: %w", err)
	}
	versionID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read version id: %w", err)
	}
	for _, file := range files {
		sum := sha256.Sum256(file.Content)
		fingerprint := hex.EncodeToString(sum[:])
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
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, git_tree_sha FROM skill_versions WHERE skill_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		version := SkillVersion{SkillID: id}
		if err := rows.Scan(&version.ID, &version.GitTreeSHA); err != nil {
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
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	version = SkillVersion{ID: id}
	if err := l.db.QueryRowContext(ctx, `SELECT skill_id, git_tree_sha FROM skill_versions WHERE id = ?`, id).Scan(&version.SkillID, &version.GitTreeSHA); err != nil {
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
	ctx, unlock := l.readOperation(ctx)
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
		return VersionDiff{}, &Error{Code: InvalidArgument, Message: "diff skill versions: versions belong to different skills"}
	}
	oldFiles := make(map[string][sha256.Size]byte, len(oldVersion.Files))
	for _, file := range oldVersion.Files {
		oldFiles[file.Path] = sha256.Sum256(file.Content)
	}
	newFiles := make(map[string][sha256.Size]byte, len(newVersion.Files))
	for _, file := range newVersion.Files {
		newFiles[file.Path] = sha256.Sum256(file.Content)
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
