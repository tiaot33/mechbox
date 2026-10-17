package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type SkippedChange struct {
	SkillID int64
	Content string
}

type RecordSkill struct {
	SkillID  int64
	Name     string
	FromName string
	From     int64
	To       int64
}

type RecordInstall struct {
	SkillID int64
	Scope   Scope
	Outcome string
	Error   string
}

type Record struct {
	ID            int64
	SourceID      int64
	Kind          string
	Revision      string
	WholeRevision bool
	Commits       string
	FromScope     *Scope
	RollsBack     int64
	RolledBackBy  int64
	At            time.Time
	Skills        []RecordSkill
	Skipped       []SkippedChange
	Installs      []RecordInstall
}

func (t *Tx) AddRecord(ctx context.Context, record Record) (int64, error) {
	var rollsBack sql.NullInt64
	if record.RollsBack != 0 {
		rollsBack = sql.NullInt64{Int64: record.RollsBack, Valid: true}
	}
	var fromAgent string
	var fromProject *int64
	if record.FromScope != nil {
		fromAgent, fromProject = record.FromScope.Agent, record.FromScope.ProjectID
	}
	result, err := t.q.ExecContext(ctx, `
		INSERT INTO update_history(source_id, kind, revision, whole_revision, commits, from_agent, from_project_id, rolls_back, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.SourceID, record.Kind, record.Revision, record.WholeRevision, record.Commits, fromAgent, fromProject, rollsBack, formatTime(record.At))
	if err != nil {
		return 0, fmt.Errorf("record %s: %w", record.Kind, wrap(err))
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read record id: %w", wrap(err))
	}
	for _, skill := range record.Skills {
		if _, err := t.q.ExecContext(ctx, `INSERT INTO history_skills(record_id, skill_id, name, from_name, from_version_id, to_version_id) VALUES (?, ?, ?, ?, ?, ?)`, id, skill.SkillID, skill.Name, skill.FromName, skill.From, skill.To); err != nil {
			return 0, fmt.Errorf("record skill: %w", wrap(err))
		}
	}
	for _, change := range record.Skipped {
		if _, err := t.q.ExecContext(ctx, `INSERT INTO skipped_changes(record_id, skill_id, content) VALUES (?, ?, ?)`, id, change.SkillID, change.Content); err != nil {
			return 0, fmt.Errorf("record skipped change: %w", wrap(err))
		}
	}
	for _, install := range record.Installs {
		if err := t.RecordInstall(ctx, id, install); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (t *Tx) RecordInstall(ctx context.Context, recordID int64, install RecordInstall) error {
	if _, err := t.q.ExecContext(ctx, `
		DELETE FROM history_installs WHERE record_id = ? AND skill_id = ? AND agent = ? AND project_id IS ?
			AND NOT (? = 'failed' AND outcome IN ('reinstalled', 'current'))
	`, recordID, install.SkillID, install.Scope.Agent, install.Scope.ProjectID, install.Outcome); err != nil {
		return fmt.Errorf("record install: %w", wrap(err))
	}
	if _, err := t.q.ExecContext(ctx, `INSERT INTO history_installs(record_id, skill_id, agent, project_id, outcome, error) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, recordID, install.SkillID, install.Scope.Agent, install.Scope.ProjectID, install.Outcome, install.Error); err != nil {
		return fmt.Errorf("record install: %w", wrap(err))
	}
	return nil
}

func (q queries) Skipped(ctx context.Context, skillID int64, content string) (bool, error) {
	var skipped bool
	if err := q.q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM skipped_changes WHERE skill_id = ? AND content = ?)`, skillID, content).Scan(&skipped); err != nil {
		return false, fmt.Errorf("read skipped change: %w", wrap(err))
	}
	return skipped, nil
}

const recordQuery = `
	SELECT h.id, h.source_id, h.kind, h.revision, h.whole_revision, h.commits, h.from_agent, h.from_project_id, ifnull(h.rolls_back, 0),
		ifnull((SELECT r.id FROM update_history r WHERE r.rolls_back = h.id), 0), h.created_at
	FROM update_history h
`

func (q queries) GetRecord(ctx context.Context, id int64) (Record, error) {
	records, err := q.listRecords(ctx, `WHERE h.id = ?`, id)
	if err != nil {
		return Record{}, err
	}
	if len(records) == 0 {
		return Record{}, fmt.Errorf("get update record: %w", ErrNotFound)
	}
	return records[0], nil
}

func (q queries) ListRecords(ctx context.Context, sourceID, skillID int64) ([]Record, error) {
	var clauses []string
	var args []any
	if sourceID != 0 {
		clauses = append(clauses, `h.source_id = ?`)
		args = append(args, sourceID)
	}
	if skillID != 0 {
		clauses = append(clauses, `EXISTS (SELECT 1 FROM history_skills hs WHERE hs.record_id = h.id AND hs.skill_id = ?)`)
		args = append(args, skillID)
	}
	clause := ""
	if len(clauses) > 0 {
		clause = `WHERE ` + strings.Join(clauses, ` AND `)
	}
	return q.listRecords(ctx, clause+` ORDER BY h.id DESC`, args...)
}

func (q queries) listRecords(ctx context.Context, clause string, args ...any) ([]Record, error) {
	rows, err := q.q.QueryContext(ctx, recordQuery+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("list update records: %w", wrap(err))
	}
	var records []Record
	for rows.Next() {
		var record Record
		var at, fromAgent string
		var fromProject sql.NullInt64
		if err := rows.Scan(&record.ID, &record.SourceID, &record.Kind, &record.Revision, &record.WholeRevision, &record.Commits, &fromAgent, &fromProject, &record.RollsBack, &record.RolledBackBy, &at); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read update record: %w", wrap(err))
		}
		if record.At, err = parseTime(at); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode update record time: %w", err)
		}
		if fromAgent != "" {
			record.FromScope = &Scope{Agent: fromAgent}
			if fromProject.Valid {
				record.FromScope.ProjectID = &fromProject.Int64
			}
		}
		records = append(records, record)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("list update records: %w", wrap(err))
	}
	for i := range records {
		if records[i].Skills, err = q.recordSkills(ctx, records[i].ID); err != nil {
			return nil, err
		}
		if records[i].Installs, err = q.recordInstalls(ctx, records[i].ID); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func (q queries) recordSkills(ctx context.Context, id int64) ([]RecordSkill, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT skill_id, name, from_name, from_version_id, to_version_id FROM history_skills WHERE record_id = ? ORDER BY skill_id`, id)
	if err != nil {
		return nil, fmt.Errorf("list record skills: %w", wrap(err))
	}
	defer rows.Close()
	var skills []RecordSkill
	for rows.Next() {
		var skill RecordSkill
		if err := rows.Scan(&skill.SkillID, &skill.Name, &skill.FromName, &skill.From, &skill.To); err != nil {
			return nil, fmt.Errorf("read record skill: %w", wrap(err))
		}
		skills = append(skills, skill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list record skills: %w", wrap(err))
	}
	return skills, nil
}

func (q queries) recordInstalls(ctx context.Context, id int64) ([]RecordInstall, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT skill_id, agent, project_id, outcome, error FROM history_installs WHERE record_id = ? ORDER BY skill_id, agent, project_id IS NOT NULL, project_id`, id)
	if err != nil {
		return nil, fmt.Errorf("list record installs: %w", wrap(err))
	}
	defer rows.Close()
	var installs []RecordInstall
	for rows.Next() {
		var install RecordInstall
		var project sql.NullInt64
		if err := rows.Scan(&install.SkillID, &install.Scope.Agent, &project, &install.Outcome, &install.Error); err != nil {
			return nil, fmt.Errorf("read record install: %w", wrap(err))
		}
		if project.Valid {
			install.Scope.ProjectID = &project.Int64
		}
		installs = append(installs, install)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list record installs: %w", wrap(err))
	}
	return installs, nil
}

func (t *Tx) SetCurrentVersion(ctx context.Context, skillID, versionID int64) error {
	result, err := t.q.ExecContext(ctx, `
		UPDATE skills SET current_version_id = ?
		WHERE id = ? AND EXISTS (SELECT 1 FROM skill_versions WHERE id = ? AND skill_id = ?)
	`, versionID, skillID, versionID, skillID)
	if err != nil {
		return fmt.Errorf("set current version: %w", wrap(err))
	}
	if changed, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("set current version: %w", wrap(err))
	} else if changed == 0 {
		return fmt.Errorf("set current version: %w", ErrNotFound)
	}
	return nil
}

func (q queries) Replica(ctx context.Context) (string, error) {
	var token string
	if err := q.q.QueryRowContext(ctx, `SELECT token FROM replica`).Scan(&token); err != nil {
		return "", fmt.Errorf("read replica: %w", wrap(err))
	}
	return token, nil
}

func (t *Tx) RequireReplica(ctx context.Context, token string) error {
	current, err := t.Replica(ctx)
	if err != nil {
		return err
	}
	if current != token {
		return ErrReplaced
	}
	return nil
}
