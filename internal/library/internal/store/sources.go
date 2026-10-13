package store

import (
	"context"
	"fmt"
	"slices"
)

type SourceSkills struct {
	SourceID int64
	Seen     map[string]bool
	Fetched  map[string]int64
}

func (q queries) SourceSkills(ctx context.Context, sourceType string, locator []byte) (SourceSkills, error) {
	result := SourceSkills{Seen: map[string]bool{}, Fetched: map[string]int64{}}
	if err := q.q.QueryRowContext(ctx, `SELECT coalesce((SELECT id FROM sources WHERE type = ? AND locator = ?), 0)`, sourceType, locator).Scan(&result.SourceID); err != nil {
		return SourceSkills{}, fmt.Errorf("find source: %w", wrap(err))
	}
	if result.SourceID == 0 {
		return result, nil
	}
	rows, err := q.q.QueryContext(ctx, `
		SELECT name, 0 FROM source_skills WHERE source_id = ? AND new = 0
		UNION ALL
		SELECT name, id FROM skills WHERE source_id = ?
	`, result.SourceID, result.SourceID)
	if err != nil {
		return SourceSkills{}, fmt.Errorf("list source skills: %w", wrap(err))
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			return SourceSkills{}, fmt.Errorf("read source skill: %w", wrap(err))
		}
		if id == 0 {
			result.Seen[name] = true
		} else {
			result.Fetched[name] = id
		}
	}
	if err := rows.Err(); err != nil {
		return SourceSkills{}, fmt.Errorf("list source skills: %w", wrap(err))
	}
	return result, nil
}

type Listed struct {
	Revision string
	Serial   int64
}

func (q queries) Listed(ctx context.Context, sourceID int64) (Listed, error) {
	var listed Listed
	if err := q.q.QueryRowContext(ctx, `SELECT listed_revision, listed_serial FROM sources WHERE id = ?`, sourceID).Scan(&listed.Revision, &listed.Serial); err != nil {
		return Listed{}, fmt.Errorf("read listed source skills: %w", wrap(err))
	}
	return listed, nil
}

func (q queries) NewSkills(ctx context.Context) (map[int64][]string, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT source_skills.source_id, source_skills.name FROM source_skills
		WHERE source_skills.new = 1 AND NOT EXISTS (
			SELECT 1 FROM skills WHERE skills.source_id = source_skills.source_id
				AND (skills.name = source_skills.name OR skills.update_check_status = 'deleted' AND skills.update_check_renamed_to = source_skills.name)
		)
		ORDER BY source_skills.source_id, source_skills.name
	`)
	if err != nil {
		return nil, fmt.Errorf("list new skills: %w", wrap(err))
	}
	defer rows.Close()
	result := map[int64][]string{}
	for rows.Next() {
		var sourceID int64
		var name string
		if err := rows.Scan(&sourceID, &name); err != nil {
			return nil, fmt.Errorf("read new skill: %w", wrap(err))
		}
		result[sourceID] = append(result[sourceID], name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list new skills: %w", wrap(err))
	}
	return result, nil
}

func (t *Tx) MarkSeen(ctx context.Context, sourceID int64, names []string) error {
	for _, name := range names {
		if _, err := t.q.ExecContext(ctx, `
			INSERT INTO source_skills(source_id, name, new) VALUES (?, ?, 0)
			ON CONFLICT(source_id, name) DO UPDATE SET new = 0
		`, sourceID, name); err != nil {
			return fmt.Errorf("mark source skill seen: %w", wrap(err))
		}
	}
	return nil
}

func (t *Tx) ListSourceSkills(ctx context.Context, sourceID int64, names []string, revision string, seen bool) error {
	if seen {
		if err := t.MarkSeen(ctx, sourceID, names); err != nil {
			return err
		}
	} else {
		for _, name := range names {
			if _, err := t.q.ExecContext(ctx, `INSERT INTO source_skills(source_id, name, new) VALUES (?, ?, 1) ON CONFLICT(source_id, name) DO NOTHING`, sourceID, name); err != nil {
				return fmt.Errorf("record new skill: %w", wrap(err))
			}
		}
	}
	rows, err := t.q.QueryContext(ctx, `SELECT name FROM source_skills WHERE source_id = ? AND new = 1`, sourceID)
	if err != nil {
		return fmt.Errorf("list new skills: %w", wrap(err))
	}
	var gone []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("read new skill: %w", wrap(err))
		}
		if !slices.Contains(names, name) {
			gone = append(gone, name)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("list new skills: %w", wrap(err))
	}
	for _, name := range gone {
		if _, err := t.q.ExecContext(ctx, `DELETE FROM source_skills WHERE source_id = ? AND name = ? AND new = 1`, sourceID, name); err != nil {
			return fmt.Errorf("forget new skill: %w", wrap(err))
		}
	}
	if _, err := t.q.ExecContext(ctx, `UPDATE sources SET listed_revision = ?, listed_serial = listed_serial + 1 WHERE id = ?`, revision, sourceID); err != nil {
		return fmt.Errorf("record listed revision: %w", wrap(err))
	}
	return nil
}

func (t *Tx) ConfirmListed(ctx context.Context, sourceID int64) error {
	if _, err := t.q.ExecContext(ctx, `UPDATE sources SET listed_serial = listed_serial + 1 WHERE id = ?`, sourceID); err != nil {
		return fmt.Errorf("confirm listed source skills: %w", wrap(err))
	}
	return nil
}

func (t *Tx) AddNewSkill(ctx context.Context, sourceID int64, name string) error {
	if _, err := t.q.ExecContext(ctx, `INSERT INTO source_skills(source_id, name, new) VALUES (?, ?, 1) ON CONFLICT(source_id, name) DO NOTHING`, sourceID, name); err != nil {
		return fmt.Errorf("record new skill: %w", wrap(err))
	}
	return nil
}

func (t *Tx) MarkNew(ctx context.Context, sourceID int64, name string) error {
	if _, err := t.q.ExecContext(ctx, `
		INSERT INTO source_skills(source_id, name, new) VALUES (?, ?, 1)
		ON CONFLICT(source_id, name) DO UPDATE SET new = 1
	`, sourceID, name); err != nil {
		return fmt.Errorf("record new skill: %w", wrap(err))
	}
	return nil
}
