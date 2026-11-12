package store

import (
	"context"
	"fmt"
)

func (q queries) SourceOrder(ctx context.Context) ([]int64, error) {
	return q.positionOrder(ctx, "sources")
}

func (q queries) GroupOrder(ctx context.Context) ([]int64, error) {
	return q.positionOrder(ctx, "groups")
}

func (t *Tx) SetSourcePositions(ctx context.Context, ids []int64) error {
	return t.setPositions(ctx, "sources", ids)
}

func (t *Tx) SetGroupPositions(ctx context.Context, ids []int64) error {
	return t.setPositions(ctx, "groups", ids)
}

func (q queries) positionOrder(ctx context.Context, table string) ([]int64, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id FROM `+table+` ORDER BY position = 0, position, id`)
	if err != nil {
		return nil, fmt.Errorf("list %s order: %w", table, wrap(err))
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read %s order: %w", table, wrap(err))
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s order: %w", table, wrap(err))
	}
	return ids, nil
}

func (t *Tx) setPositions(ctx context.Context, table string, ids []int64) error {
	if _, err := t.q.ExecContext(ctx, `UPDATE `+table+` SET position = 0`); err != nil {
		return fmt.Errorf("reset %s order: %w", table, wrap(err))
	}
	for i, id := range ids {
		if _, err := t.q.ExecContext(ctx, `UPDATE `+table+` SET position = ? WHERE id = ?`, i+1, id); err != nil {
			return fmt.Errorf("set %s order: %w", table, wrap(err))
		}
	}
	return nil
}
