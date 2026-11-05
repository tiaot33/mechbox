package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (q queries) MentionJudgments(ctx context.Context) (map[int64]map[string]bool, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT version_id, name, mentioned FROM mention_judgments
		WHERE version_id IN (SELECT current_version_id FROM skills)
	`)
	if err != nil {
		return nil, fmt.Errorf("list mention judgments: %w", wrap(err))
	}
	defer rows.Close()
	judgments := map[int64]map[string]bool{}
	for rows.Next() {
		var version int64
		var name string
		var mentioned bool
		if err := rows.Scan(&version, &name, &mentioned); err != nil {
			return nil, fmt.Errorf("read mention judgment: %w", wrap(err))
		}
		if judgments[version] == nil {
			judgments[version] = map[string]bool{}
		}
		judgments[version][name] = mentioned
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read mention judgments: %w", wrap(err))
	}
	return judgments, nil
}

func (q queries) SetMentionJudgments(ctx context.Context, versionID int64, judgments map[string]bool, at time.Time) error {
	if len(judgments) == 0 {
		return nil
	}
	encoded, err := json.Marshal(judgments)
	if err != nil {
		return fmt.Errorf("encode mention judgments: %w", err)
	}
	result, err := q.q.ExecContext(ctx, `
		INSERT INTO mention_judgments(version_id, name, mentioned, judged_at)
		SELECT skill_versions.id, judgment.key, judgment.value, ? FROM skill_versions, json_each(?) AS judgment
		WHERE skill_versions.id = ?
		ON CONFLICT(version_id, name) DO UPDATE SET mentioned = excluded.mentioned, judged_at = excluded.judged_at
	`, formatTime(at), string(encoded), versionID)
	if err != nil {
		return fmt.Errorf("set mention judgments: %w", wrap(err))
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("set mention judgments: %w", errors.Join(ErrNotFound, err))
	}
	return nil
}
