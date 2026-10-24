package store

import (
	"context"
	"errors"
	"fmt"
)

func (q queries) GitHubToken(ctx context.Context) (string, error) {
	var token string
	err := q.q.QueryRowContext(ctx, `SELECT token FROM github_token WHERE id = 1`).Scan(&token)
	if err = wrap(err); errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get github token: %w", err)
	}
	return token, nil
}

func (q queries) SetGitHubToken(ctx context.Context, token string) error {
	var err error
	if token == "" {
		_, err = q.q.ExecContext(ctx, `DELETE FROM github_token`)
	} else {
		_, err = q.q.ExecContext(ctx, `INSERT INTO github_token(id, token) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET token = excluded.token`, token)
	}
	if err != nil {
		return fmt.Errorf("set github token: %w", wrap(err))
	}
	return nil
}
