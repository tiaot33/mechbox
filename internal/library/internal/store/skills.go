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
	ID      int64
	Type    string
	Locator []byte
}

type Skill struct {
	ID            int64
	Name          string
	Description   string
	Source        Source
	VersionID     int64
	Revision      string
	FetchedAt     time.Time
	UpdatedAt     time.Time
	CheckedAt     time.Time
	CheckStatus   string
	CheckRevision string
	Skipped       bool
	KeptDeleted   bool
}

type Current struct {
	VersionID int64
	State     string
	Revision  string
	CheckedAt time.Time
	Files     []skillfile.File
}

type NewVersion struct {
	Description string
	Files       []skillfile.File
	State       string
	Revision    string
	CreatedAt   time.Time
}

const skillQuery = `
	SELECT skills.id, skills.name, current_version.description,
		sources.id, sources.type, sources.locator, current_version.id, current_version.revision,
		first_version.created_at, CASE WHEN current_version.id = first_version.id THEN '' ELSE current_version.created_at END,
		skills.update_checked_at, skills.update_check_status, skills.update_check_revision,
		skills.update_check_content != '' AND EXISTS (
			SELECT 1 FROM skipped_changes WHERE skipped_changes.skill_id = skills.id AND skipped_changes.content = skills.update_check_content
		),
		skills.kept_deleted
	FROM skills
	JOIN sources ON sources.id = skills.source_id
	JOIN skill_versions AS current_version ON current_version.id = skills.current_version_id
	JOIN skill_versions AS first_version ON first_version.id = (
		SELECT id FROM skill_versions WHERE skill_id = skills.id ORDER BY id LIMIT 1
	)
`

func (q queries) GetSkill(ctx context.Context, id int64) (Skill, error) {
	skill, err := scanSkill(q.q.QueryRowContext(ctx, skillQuery+`WHERE skills.id = ?`, id))
	if err != nil {
		return Skill{}, fmt.Errorf("get skill: %w", wrap(err))
	}
	return skill, nil
}

func (q queries) ListSkills(ctx context.Context) ([]Skill, error) {
	return q.listSkills(ctx, "list skills", `ORDER BY skills.name, skills.id`)
}

func (q queries) ListGroupSkills(ctx context.Context, groupID int64) ([]Skill, error) {
	return q.listSkills(ctx, "list group skills", `JOIN skill_groups ON skill_groups.skill_id = skills.id WHERE skill_groups.group_id = ? ORDER BY skills.id`, groupID)
}

func (q queries) ListCollectionSkills(ctx context.Context, sourceID int64) ([]Skill, error) {
	return q.listSkills(ctx, "list collection skills", `WHERE skills.source_id = ? ORDER BY skills.id`, sourceID)
}

func (q queries) GetCollectionSource(ctx context.Context, sourceID int64) (Source, error) {
	source := Source{ID: sourceID}
	if err := q.q.QueryRowContext(ctx, `
		SELECT type, locator FROM sources
		WHERE id = ? AND EXISTS (SELECT 1 FROM skills WHERE skills.source_id = sources.id)
	`, sourceID).Scan(&source.Type, &source.Locator); err != nil {
		return Source{}, fmt.Errorf("get collection source: %w", wrap(err))
	}
	return source, nil
}

func (q queries) listSkills(ctx context.Context, operation, clause string, args ...any) ([]Skill, error) {
	rows, err := q.q.QueryContext(ctx, skillQuery+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, wrap(err))
	}
	defer rows.Close()
	var skills []Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, wrap(err))
		}
		skills = append(skills, skill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, wrap(err))
	}
	return skills, nil
}

func scanSkill(row interface{ Scan(...any) error }) (Skill, error) {
	var skill Skill
	var fetchedAt, updatedAt, checkedAt string
	err := row.Scan(&skill.ID, &skill.Name, &skill.Description, &skill.Source.ID, &skill.Source.Type, &skill.Source.Locator, &skill.VersionID, &skill.Revision, &fetchedAt, &updatedAt, &checkedAt, &skill.CheckStatus, &skill.CheckRevision, &skill.Skipped, &skill.KeptDeleted)
	if err != nil {
		return Skill{}, err
	}
	if skill.FetchedAt, err = parseTime(fetchedAt); err != nil {
		return Skill{}, fmt.Errorf("decode fetched at: %w", err)
	}
	if skill.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Skill{}, fmt.Errorf("decode updated at: %w", err)
	}
	if skill.CheckStatus != "" {
		if skill.CheckedAt, err = parseTime(checkedAt); err != nil {
			return Skill{}, fmt.Errorf("decode update checked at: %w", err)
		}
	}
	return skill, nil
}

func (q queries) GetCurrent(ctx context.Context, skillID int64) (Current, error) {
	var current Current
	var checkedAt string
	if err := q.q.QueryRowContext(ctx, `
		SELECT skill_versions.id, skill_versions.source_state, skill_versions.revision, skills.update_checked_at
		FROM skills
		JOIN skill_versions ON skill_versions.id = skills.current_version_id
		WHERE skills.id = ?
	`, skillID).Scan(&current.VersionID, &current.State, &current.Revision, &checkedAt); err != nil {
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

func (q queries) RequireSkill(ctx context.Context, id int64) error {
	var exists int
	if err := q.q.QueryRowContext(ctx, `SELECT 1 FROM skills WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get skill: %w", wrap(err))
	}
	return nil
}

func (t *Tx) UpsertSource(ctx context.Context, source Source) (int64, error) {
	if _, err := t.q.ExecContext(ctx, `
		INSERT INTO sources(type, locator) VALUES (?, ?)
		ON CONFLICT(type, locator) DO NOTHING
	`, source.Type, source.Locator); err != nil {
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
		`DELETE FROM skipped_changes WHERE skill_id = ?`,
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

func (t *Tx) ConfirmVersion(ctx context.Context, skillID int64, state, revision string) error {
	if _, err := t.q.ExecContext(ctx, `
		UPDATE skill_versions SET source_state = ?, revision = ?
		WHERE id = (SELECT current_version_id FROM skills WHERE id = ?)
	`, state, revision, skillID); err != nil {
		return fmt.Errorf("confirm current version: %w", wrap(err))
	}
	return nil
}

func (t *Tx) UpdateCheck(ctx context.Context, skillID int64) (UpdateCheck, error) {
	var check UpdateCheck
	var checkedAt string
	if err := t.q.QueryRowContext(ctx, `SELECT update_checked_at, update_check_status, update_check_revision, update_check_content FROM skills WHERE id = ?`, skillID).Scan(&checkedAt, &check.Status, &check.Revision, &check.Content); err != nil {
		return UpdateCheck{}, fmt.Errorf("read update check: %w", wrap(err))
	}
	var err error
	if check.At, err = parseTime(checkedAt); err != nil {
		return UpdateCheck{}, fmt.Errorf("decode update checked at: %w", err)
	}
	return check, nil
}

type UpdateCheck struct {
	At       time.Time
	Status   string
	Revision string
	Content  string
	Found    bool
}

func (t *Tx) SaveUpdateCheck(ctx context.Context, skillID int64, check UpdateCheck) error {
	if _, err := t.q.ExecContext(ctx, `
		UPDATE skills SET update_checked_at = ?, update_check_status = ?, update_check_revision = ?, update_check_content = ?,
			kept_deleted = CASE WHEN ? THEN 0 ELSE kept_deleted END
		WHERE id = ?
	`, formatTime(check.At), check.Status, check.Revision, check.Content, check.Found, skillID); err != nil {
		return fmt.Errorf("save update check: %w", wrap(err))
	}
	return nil
}

func (t *Tx) KeepDeleted(ctx context.Context, skillID int64) error {
	result, err := t.q.ExecContext(ctx, `UPDATE skills SET kept_deleted = 1 WHERE id = ?`, skillID)
	if err != nil {
		return fmt.Errorf("keep deleted skill: %w", wrap(err))
	}
	if changed, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("keep deleted skill: %w", wrap(err))
	} else if changed == 0 {
		return fmt.Errorf("keep deleted skill: %w", ErrNotFound)
	}
	return nil
}
