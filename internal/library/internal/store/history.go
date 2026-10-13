package store

import (
	"context"
	"fmt"
	"time"
)

type SkippedChange struct {
	SkillID int64
	Content string
}

type Skip struct {
	SourceID      int64
	Revision      string
	WholeRevision bool
	At            time.Time
	Changes       []SkippedChange
}

func (t *Tx) RecordSkip(ctx context.Context, skip Skip) (int64, error) {
	result, err := t.q.ExecContext(ctx, `INSERT INTO update_history(source_id, kind, revision, whole_revision, created_at) VALUES (?, 'skip', ?, ?, ?)`, skip.SourceID, skip.Revision, skip.WholeRevision, formatTime(skip.At))
	if err != nil {
		return 0, fmt.Errorf("record skip: %w", wrap(err))
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read skip id: %w", wrap(err))
	}
	for _, change := range skip.Changes {
		if _, err := t.q.ExecContext(ctx, `INSERT INTO skipped_changes(record_id, skill_id, content) VALUES (?, ?, ?)`, id, change.SkillID, change.Content); err != nil {
			return 0, fmt.Errorf("record skipped change: %w", wrap(err))
		}
	}
	return id, nil
}

func (q queries) Skipped(ctx context.Context, skillID int64, content string) (bool, error) {
	var skipped bool
	if err := q.q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM skipped_changes WHERE skill_id = ? AND content = ?)`, skillID, content).Scan(&skipped); err != nil {
		return false, fmt.Errorf("read skipped change: %w", wrap(err))
	}
	return skipped, nil
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
