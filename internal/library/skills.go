package library

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

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

func (l *sqliteLibrary) getSummary(ctx context.Context, id SkillID) (SkillSummary, error) {
	summary, err := scanSummary(l.db.QueryRowContext(ctx, summaryQuery+`WHERE skills.id = ?`, id))
	if err != nil {
		return SkillSummary{}, fmt.Errorf("get skill: %w", err)
	}
	return summary, nil
}

func (l *sqliteLibrary) listSummaries(ctx context.Context, operation, clause string, args ...any) ([]SkillSummary, error) {
	rows, err := l.db.QueryContext(ctx, summaryQuery+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	defer rows.Close()
	var skills []SkillSummary
	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		skills = append(skills, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	return skills, nil
}

func scanSummary(row interface{ Scan(...any) error }) (SkillSummary, error) {
	var summary SkillSummary
	var locator []byte
	var lastFetched, fetchedAt, updatedAt string
	var checkedAt string
	var check UpdateCheckState
	err := row.Scan(&summary.ID, &summary.Name, &summary.Description, &summary.Source.ID, &summary.Source.Type, &locator, &lastFetched, &summary.Source.LastSeenRef, &fetchedAt, &updatedAt, &checkedAt, &check.Status)
	if err != nil {
		return SkillSummary{}, err
	}
	if err := json.Unmarshal(locator, &summary.Source.Locator); err != nil {
		return SkillSummary{}, fmt.Errorf("decode source: %w", err)
	}
	if summary.Source.LastFetched, err = parseStoredTime(lastFetched); err != nil {
		return SkillSummary{}, fmt.Errorf("decode last fetched: %w", err)
	}
	if summary.FetchedAt, err = parseStoredTime(fetchedAt); err != nil {
		return SkillSummary{}, fmt.Errorf("decode fetched at: %w", err)
	}
	if summary.UpdatedAt, err = parseStoredTime(updatedAt); err != nil {
		return SkillSummary{}, fmt.Errorf("decode updated at: %w", err)
	}
	if check.Status != "" {
		if check.CheckedAt, err = parseStoredTime(checkedAt); err != nil {
			return SkillSummary{}, fmt.Errorf("decode update checked at: %w", err)
		}
		summary.LastUpdateCheck = &check
	}
	return summary, nil
}

func parseStoredTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func formatStoredTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func clockNow() time.Time {
	now, _ := parseStoredTime(formatStoredTime(time.Now().UTC()))
	return now
}

func skillDescription(files []File) string {
	for _, file := range files {
		if file.Path != "SKILL.md" {
			continue
		}
		meta, err := candidateMetadata(file.Content, ".")
		if err != nil {
			return ""
		}
		return meta.Description
	}
	return ""
}

func (l *sqliteLibrary) fetchAll(ctx context.Context, source Source, skills []discoveredSkill, validator string) ([]Skill, error) {
	if err := validateSource(source); err != nil {
		return nil, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin fetch: %w", err)
	}
	defer tx.Rollback()
	sourceID, err := upsertSource(ctx, tx, source, validator)
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		id, err := findSkillBySourceName(ctx, tx, sourceID, skill.name)
		if err != nil {
			return nil, err
		}
		if id != 0 {
			return nil, existsError("fetch skill", "skill already fetched", ErrorDetails{SkillID: id, Name: skill.name})
		}
	}
	fetched := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		description := skill.candidate.Description
		if description == "" {
			description = skillDescription(skill.files)
		}
		result, err := insertSkill(ctx, tx, sourceID, source, skill.name, skill.files, sourceGitRecord(source, skill.gitTreeSHA), description)
		if err != nil {
			return nil, err
		}
		fetched = append(fetched, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit fetch: %w", err)
	}
	return fetched, nil
}

func upsertSource(ctx context.Context, tx *sql.Tx, source Source, validator string) (int64, error) {
	locator, err := json.Marshal(source.Locator)
	if err != nil {
		return 0, fmt.Errorf("encode source: %w", err)
	}
	fetched := formatStoredTime(source.LastFetched)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sources(type, locator, last_fetched, last_seen_ref, http_validator) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(type, locator) DO UPDATE SET
			last_fetched = excluded.last_fetched, last_seen_ref = excluded.last_seen_ref, http_validator = excluded.http_validator
	`, source.Type, locator, fetched, source.LastSeenRef, validator); err != nil {
		return 0, fmt.Errorf("store source: %w", err)
	}
	var sourceID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sources WHERE type = ? AND locator = ?`, source.Type, locator).Scan(&sourceID); err != nil {
		return 0, fmt.Errorf("read source: %w", err)
	}
	return sourceID, nil
}

func findSkillBySourceName(ctx context.Context, tx *sql.Tx, sourceID int64, name string) (SkillID, error) {
	var id SkillID
	err := tx.QueryRowContext(ctx, `SELECT id FROM skills WHERE source_id = ? AND name = ?`, sourceID, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find skill: %w", err)
	}
	return id, nil
}

func insertSkill(ctx context.Context, tx *sql.Tx, sourceID int64, source Source, name string, files []File, record gitRecord, description string) (Skill, error) {
	if name == "" {
		return Skill{}, fmt.Errorf("fetch skill: name is required")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO skills(source_id, name) VALUES (?, ?)`, sourceID, name)
	if err != nil {
		return Skill{}, fmt.Errorf("store skill: %w", err)
	}
	skillID, err := result.LastInsertId()
	if err != nil {
		return Skill{}, fmt.Errorf("read skill id: %w", err)
	}
	createdAt := clockNow()
	versionID, err := createVersion(ctx, tx, skillID, files, record, description, createdAt)
	if err != nil {
		return Skill{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, skillID); err != nil {
		return Skill{}, fmt.Errorf("select current version: %w", err)
	}
	source.ID = SourceID(sourceID)
	source.LastFetched = source.LastFetched.UTC()
	return Skill{ID: SkillID(skillID), Name: name, Description: description, Source: source, GitTreeSHA: record.treeSHA, FetchedAt: createdAt, UpdatedAt: createdAt, Files: files}, nil
}

func (l *sqliteLibrary) GetSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	defer func() { err = typedError(err) }()
	skill, _, err = l.getSkill(ctx, id)
	return skill, err
}

type sourceState struct {
	updateCheckedAt string
	versionID       int64
	confirmedCommit string
	httpValidator   string
}

func (l *sqliteLibrary) getSkill(ctx context.Context, id SkillID) (Skill, sourceState, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	summary, err := l.getSummary(ctx, id)
	if err != nil {
		return Skill{}, sourceState{}, err
	}
	skill := Skill{
		ID:          summary.ID,
		Name:        summary.Name,
		Description: summary.Description,
		Source:      summary.Source,
		FetchedAt:   summary.FetchedAt,
		UpdatedAt:   summary.UpdatedAt,
	}
	var state sourceState
	if err := l.db.QueryRowContext(ctx, `
		SELECT skill_versions.id, skill_versions.git_tree_sha, skill_versions.confirmed_commit, sources.http_validator, skills.update_checked_at
		FROM skills
		JOIN skill_versions ON skill_versions.id = skills.current_version_id
		JOIN sources ON sources.id = skills.source_id
		WHERE skills.id = ?
	`, id).Scan(&state.versionID, &skill.GitTreeSHA, &state.confirmedCommit, &state.httpValidator, &state.updateCheckedAt); err != nil {
		return Skill{}, sourceState{}, fmt.Errorf("get skill version: %w", err)
	}

	rows, err := l.db.QueryContext(ctx, `
		SELECT version_files.path, blobs.content, version_files.mode
		FROM version_files
		JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE version_files.version_id = ?
		ORDER BY version_files.path
	`, state.versionID)
	if err != nil {
		return Skill{}, sourceState{}, fmt.Errorf("get skill files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.Path, &file.Content, &file.Mode); err != nil {
			return Skill{}, sourceState{}, fmt.Errorf("read skill file: %w", err)
		}
		skill.Files = append(skill.Files, file)
	}
	if err := rows.Err(); err != nil {
		return Skill{}, sourceState{}, fmt.Errorf("read skill files: %w", err)
	}
	return skill, state, nil
}

func (l *sqliteLibrary) ListSkillFiles(ctx context.Context, id SkillID) (files []FileMetadata, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `
		SELECT version_files.path, length(blobs.content), version_files.mode
		FROM skills
		LEFT JOIN version_files ON version_files.version_id = skills.current_version_id
		LEFT JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE skills.id = ?
		ORDER BY version_files.path
	`, id)
	if err != nil {
		return nil, fmt.Errorf("list skill files: %w", err)
	}
	defer rows.Close()
	skillExists := false
	for rows.Next() {
		skillExists = true
		var path, mode sql.NullString
		var size sql.NullInt64
		if err := rows.Scan(&path, &size, &mode); err != nil {
			return nil, fmt.Errorf("read skill file metadata: %w", err)
		}
		if path.Valid {
			files = append(files, FileMetadata{Path: path.String, Size: size.Int64, Mode: FileMode(mode.String)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list skill files: %w", err)
	}
	if !skillExists {
		return nil, &Error{Code: NotFound, Message: "list skill files: skill not found", Details: ErrorDetails{SkillID: id}}
	}
	return files, nil
}

func (l *sqliteLibrary) GetSkillFile(ctx context.Context, id SkillID, path string) (file File, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	var fileExists bool
	var mode sql.NullString
	err = l.db.QueryRowContext(ctx, `
		SELECT version_files.path IS NOT NULL, blobs.content, version_files.mode
		FROM skills
		LEFT JOIN version_files ON version_files.version_id = skills.current_version_id AND version_files.path = ?
		LEFT JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE skills.id = ?
	`, path, id).Scan(&fileExists, &file.Content, &mode)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, &Error{Code: NotFound, Message: "get skill file: skill not found", Details: ErrorDetails{SkillID: id}}
	}
	if err != nil {
		return File{}, fmt.Errorf("get skill file: %w", err)
	}
	if !fileExists {
		return File{}, &Error{Code: NotFound, Message: "get skill file: file not found", Details: ErrorDetails{Path: path}}
	}
	file.Path, file.Mode = path, FileMode(mode.String)
	return file, nil
}

func (l *sqliteLibrary) ListSkills(ctx context.Context) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.listSummaries(ctx, "list skills", `ORDER BY skills.name, skills.id`)
}

func (l *sqliteLibrary) RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateInstallationsPolicy("remove skill", opts.Installations); err != nil {
		return err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	var trash installationTrash
	if opts.Installations != Keep {
		installations, err := l.ListInstallations(ctx)
		if err != nil {
			return err
		}
		for _, installation := range installations {
			if installation.SkillID == id {
				trash.targets = append(trash.targets, filepath.Join(installation.Directory, skill.Name))
			}
		}
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove skill: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`DELETE FROM installations WHERE skill_id = ?`,
		`DELETE FROM skill_groups WHERE skill_id = ?`,
		`UPDATE skills SET current_version_id = NULL WHERE id = ?`,
		`DELETE FROM version_files WHERE version_id IN (SELECT id FROM skill_versions WHERE skill_id = ?)`,
		`DELETE FROM skill_versions WHERE skill_id = ?`,
		`DELETE FROM skills WHERE id = ?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return fmt.Errorf("remove skill: %w", err)
		}
	}
	return trash.commit(ctx, tx, "remove skill")
}

func (l *sqliteLibrary) requireSkill(ctx context.Context, id SkillID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM skills WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get skill: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) ListCollections(ctx context.Context) (collections []Collection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	skills, err := l.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(skills, func(a, b SkillSummary) int {
		return cmp.Or(cmp.Compare(a.Source.ID, b.Source.ID), cmp.Compare(a.ID, b.ID))
	})
	for _, skill := range skills {
		if len(collections) == 0 || collections[len(collections)-1].Source.ID != skill.Source.ID {
			collections = append(collections, Collection{Source: skill.Source})
		}
		last := len(collections) - 1
		collections[last].Skills = append(collections[last].Skills, skill)
	}
	return collections, nil
}
