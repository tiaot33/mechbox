package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

type Source struct {
	ID          int64
	Type        string
	Locator     []byte
	LastFetched time.Time
	LastSeenRef string
}

type Summary struct {
	ID          int64
	Name        string
	Description string
	Source      Source
	FetchedAt   time.Time
	UpdatedAt   time.Time
	CheckedAt   time.Time
	CheckStatus string
}

type Current struct {
	VersionID int64
	State     string
	CheckedAt time.Time
	Files     []skillfile.File
}

type FileInfo struct {
	Path string
	Size int64
	Mode skillfile.Mode
}

type NewVersion struct {
	Description string
	Files       []skillfile.File
	State       string
	CreatedAt   time.Time
}

const summaryQuery = `
	SELECT skills.id, skills.name, current_version.description,
		sources.id, sources.type, sources.locator, sources.last_fetched, sources.last_seen_ref,
		first_version.created_at, current_version.created_at, skills.update_checked_at, skills.update_check_status
	FROM skills
	JOIN sources ON sources.id = skills.source_id
	JOIN skill_versions AS current_version ON current_version.id = skills.current_version_id
	JOIN skill_versions AS first_version ON first_version.id = (
		SELECT id FROM skill_versions WHERE skill_id = skills.id ORDER BY id LIMIT 1
	)
`

func (q queries) GetSummary(ctx context.Context, id int64) (Summary, error) {
	summary, err := scanSummary(q.q.QueryRowContext(ctx, summaryQuery+`WHERE skills.id = ?`, id))
	if err != nil {
		return Summary{}, fmt.Errorf("get skill: %w", wrap(err))
	}
	return summary, nil
}

func (q queries) ListSummaries(ctx context.Context) ([]Summary, error) {
	return q.listSummaries(ctx, "list skills", `ORDER BY skills.name, skills.id`)
}

func (q queries) ListGroupSummaries(ctx context.Context, groupID int64) ([]Summary, error) {
	return q.listSummaries(ctx, "list group skills", `JOIN skill_groups ON skill_groups.skill_id = skills.id WHERE skill_groups.group_id = ? ORDER BY skills.id`, groupID)
}

func (q queries) listSummaries(ctx context.Context, operation, clause string, args ...any) ([]Summary, error) {
	rows, err := q.q.QueryContext(ctx, summaryQuery+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, wrap(err))
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, wrap(err))
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, wrap(err))
	}
	return summaries, nil
}

func scanSummary(row interface{ Scan(...any) error }) (Summary, error) {
	var summary Summary
	var lastFetched, fetchedAt, updatedAt, checkedAt string
	err := row.Scan(&summary.ID, &summary.Name, &summary.Description, &summary.Source.ID, &summary.Source.Type, &summary.Source.Locator, &lastFetched, &summary.Source.LastSeenRef, &fetchedAt, &updatedAt, &checkedAt, &summary.CheckStatus)
	if err != nil {
		return Summary{}, err
	}
	if summary.Source.LastFetched, err = parseTime(lastFetched); err != nil {
		return Summary{}, fmt.Errorf("decode last fetched: %w", err)
	}
	if summary.FetchedAt, err = parseTime(fetchedAt); err != nil {
		return Summary{}, fmt.Errorf("decode fetched at: %w", err)
	}
	if summary.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Summary{}, fmt.Errorf("decode updated at: %w", err)
	}
	if summary.CheckStatus != "" {
		if summary.CheckedAt, err = parseTime(checkedAt); err != nil {
			return Summary{}, fmt.Errorf("decode update checked at: %w", err)
		}
	}
	return summary, nil
}

func (q queries) GetCurrent(ctx context.Context, skillID int64) (Current, error) {
	var current Current
	var checkedAt string
	if err := q.q.QueryRowContext(ctx, `
		SELECT skill_versions.id, skill_versions.source_state, skills.update_checked_at
		FROM skills
		JOIN skill_versions ON skill_versions.id = skills.current_version_id
		WHERE skills.id = ?
	`, skillID).Scan(&current.VersionID, &current.State, &checkedAt); err != nil {
		return Current{}, fmt.Errorf("get skill version: %w", wrap(err))
	}
	var err error
	if current.CheckedAt, err = parseTime(checkedAt); err != nil {
		return Current{}, fmt.Errorf("decode update checked at: %w", err)
	}
	if current.Files, err = q.versionFiles(ctx, current.VersionID, "get skill files"); err != nil {
		return Current{}, err
	}
	return current, nil
}

func (q queries) versionFiles(ctx context.Context, versionID int64, operation string) ([]skillfile.File, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT version_files.path, blobs.content, version_files.mode
		FROM version_files
		JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE version_files.version_id = ?
		ORDER BY version_files.path
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, wrap(err))
	}
	defer rows.Close()
	var files []skillfile.File
	for rows.Next() {
		var file skillfile.File
		if err := rows.Scan(&file.Path, &file.Content, &file.Mode); err != nil {
			return nil, fmt.Errorf("%s: %w", operation, wrap(err))
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, wrap(err))
	}
	return files, nil
}

func (q queries) ListCurrentFiles(ctx context.Context, skillID int64) ([]FileInfo, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT version_files.path, length(blobs.content), version_files.mode
		FROM skills
		LEFT JOIN version_files ON version_files.version_id = skills.current_version_id
		LEFT JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE skills.id = ?
		ORDER BY version_files.path
	`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list skill files: %w", wrap(err))
	}
	defer rows.Close()
	skillExists := false
	var files []FileInfo
	for rows.Next() {
		skillExists = true
		var path, mode sql.NullString
		var size sql.NullInt64
		if err := rows.Scan(&path, &size, &mode); err != nil {
			return nil, fmt.Errorf("read skill file metadata: %w", wrap(err))
		}
		if path.Valid {
			files = append(files, FileInfo{Path: path.String, Size: size.Int64, Mode: skillfile.Mode(mode.String)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list skill files: %w", wrap(err))
	}
	if !skillExists {
		return nil, fmt.Errorf("list skill files: %w", ErrNotFound)
	}
	return files, nil
}

func (q queries) GetCurrentFile(ctx context.Context, skillID int64, path string) (skillfile.File, bool, error) {
	var fileExists bool
	var mode sql.NullString
	file := skillfile.File{Path: path}
	err := q.q.QueryRowContext(ctx, `
		SELECT version_files.path IS NOT NULL, blobs.content, version_files.mode
		FROM skills
		LEFT JOIN version_files ON version_files.version_id = skills.current_version_id AND version_files.path = ?
		LEFT JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE skills.id = ?
	`, path, skillID).Scan(&fileExists, &file.Content, &mode)
	if err != nil {
		return skillfile.File{}, false, fmt.Errorf("get skill file: %w", wrap(err))
	}
	if !fileExists {
		return skillfile.File{}, false, nil
	}
	file.Mode = skillfile.Mode(mode.String)
	return file, true, nil
}

func (q queries) RequireSkill(ctx context.Context, id int64) error {
	var exists int
	if err := q.q.QueryRowContext(ctx, `SELECT 1 FROM skills WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get skill: %w", wrap(err))
	}
	return nil
}

func (q queries) SkillsFromSource(ctx context.Context, sourceType string, locator []byte) (map[string]int64, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT s.name, s.id FROM skills s JOIN sources src ON src.id = s.source_id WHERE src.type = ? AND src.locator = ?`, sourceType, locator)
	if err != nil {
		return nil, fmt.Errorf("list fetched candidates: %w", wrap(err))
	}
	defer rows.Close()
	fetched := make(map[string]int64)
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			return nil, fmt.Errorf("read fetched candidate: %w", wrap(err))
		}
		fetched[name] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read fetched candidates: %w", wrap(err))
	}
	return fetched, nil
}

func (t *Tx) UpsertSource(ctx context.Context, source Source) (int64, error) {
	if _, err := t.q.ExecContext(ctx, `
		INSERT INTO sources(type, locator, last_fetched, last_seen_ref) VALUES (?, ?, ?, ?)
		ON CONFLICT(type, locator) DO UPDATE SET
			last_fetched = excluded.last_fetched, last_seen_ref = excluded.last_seen_ref
	`, source.Type, source.Locator, formatTime(source.LastFetched), source.LastSeenRef); err != nil {
		return 0, fmt.Errorf("store source: %w", wrap(err))
	}
	var sourceID int64
	if err := t.q.QueryRowContext(ctx, `SELECT id FROM sources WHERE type = ? AND locator = ?`, source.Type, source.Locator).Scan(&sourceID); err != nil {
		return 0, fmt.Errorf("read source: %w", wrap(err))
	}
	return sourceID, nil
}

func (t *Tx) FindSkill(ctx context.Context, sourceID int64, name string) (int64, error) {
	var id int64
	err := t.q.QueryRowContext(ctx, `SELECT id FROM skills WHERE source_id = ? AND name = ?`, sourceID, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find skill: %w", wrap(err))
	}
	return id, nil
}

func (t *Tx) InsertSkill(ctx context.Context, sourceID int64, name string, version NewVersion) (skillID, versionID int64, err error) {
	result, err := t.q.ExecContext(ctx, `INSERT INTO skills(source_id, name) VALUES (?, ?)`, sourceID, name)
	if err != nil {
		return 0, 0, fmt.Errorf("store skill: %w", wrap(err))
	}
	if skillID, err = result.LastInsertId(); err != nil {
		return 0, 0, fmt.Errorf("read skill id: %w", wrap(err))
	}
	if versionID, err = t.AddVersion(ctx, skillID, version); err != nil {
		return 0, 0, err
	}
	return skillID, versionID, nil
}

func (t *Tx) RemoveSkill(ctx context.Context, id int64) error {
	statements := []string{
		`DELETE FROM skill_scopes WHERE skill_id = ?`,
		`DELETE FROM skill_groups WHERE skill_id = ?`,
		`UPDATE skills SET current_version_id = NULL WHERE id = ?`,
		`DELETE FROM version_files WHERE version_id IN (SELECT id FROM skill_versions WHERE skill_id = ?)`,
		`DELETE FROM skill_versions WHERE skill_id = ?`,
		`DELETE FROM skills WHERE id = ?`,
	}
	for _, statement := range statements {
		if _, err := t.q.ExecContext(ctx, statement, id); err != nil {
			return fmt.Errorf("remove skill: %w", wrap(err))
		}
	}
	return nil
}

func (t *Tx) CurrentVersionID(ctx context.Context, skillID int64) (int64, error) {
	var current sql.NullInt64
	if err := t.q.QueryRowContext(ctx, `SELECT current_version_id FROM skills WHERE id = ?`, skillID).Scan(&current); err != nil {
		return 0, fmt.Errorf("verify skill version: %w", wrap(err))
	}
	return current.Int64, nil
}

func (t *Tx) ConfirmVersion(ctx context.Context, skillID int64, state string) error {
	if _, err := t.q.ExecContext(ctx, `
		UPDATE skill_versions SET source_state = ?
		WHERE id = (SELECT current_version_id FROM skills WHERE id = ?)
	`, state, skillID); err != nil {
		return fmt.Errorf("confirm current version: %w", wrap(err))
	}
	return nil
}

func (t *Tx) RecordSourceFetch(ctx context.Context, sourceID int64, lastFetched time.Time, lastSeenRef string) error {
	if _, err := t.q.ExecContext(ctx, `UPDATE sources SET last_fetched = ?, last_seen_ref = ? WHERE id = ?`, formatTime(lastFetched), lastSeenRef, sourceID); err != nil {
		return fmt.Errorf("update source: %w", wrap(err))
	}
	return nil
}

func (t *Tx) ConfirmUpdateCheck(ctx context.Context, skillID int64, at time.Time, status string) error {
	if _, err := t.q.ExecContext(ctx, `UPDATE skills SET update_checked_at = ?, update_check_status = CASE WHEN update_check_status = '' THEN '' ELSE ? END WHERE id = ?`,
		formatTime(at), status, skillID); err != nil {
		return fmt.Errorf("confirm update check: %w", wrap(err))
	}
	return nil
}

func (t *Tx) UpdateCheck(ctx context.Context, skillID int64) (time.Time, string, error) {
	var checkedAt, status string
	if err := t.q.QueryRowContext(ctx, `SELECT update_checked_at, update_check_status FROM skills WHERE id = ?`, skillID).Scan(&checkedAt, &status); err != nil {
		return time.Time{}, "", fmt.Errorf("read update check time: %w", wrap(err))
	}
	at, err := parseTime(checkedAt)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decode update checked at: %w", err)
	}
	return at, status, nil
}

func (t *Tx) SaveUpdateCheck(ctx context.Context, skillID int64, at time.Time, status string) error {
	if _, err := t.q.ExecContext(ctx, `UPDATE skills SET update_checked_at = ?, update_check_status = ? WHERE id = ?`, formatTime(at), status, skillID); err != nil {
		return fmt.Errorf("save update check: %w", wrap(err))
	}
	return nil
}
