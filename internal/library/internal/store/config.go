package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	githubTokenKey = "github_token"
	wslDistroKey   = "wsl_distro"
	syncPrefix     = "sync."
	appPrefix      = "app."
)

const machineConfigWhere = `substr(key, 1, length(?)) = ? OR key = ?`

var machineConfigArgs = []any{syncPrefix, syncPrefix, wslDistroKey}

type SyncSettings struct {
	Endpoint, Bucket, Key, Region, AccessKeyID, SecretAccessKey, SessionToken string
}

func (s *SyncSettings) fields() []struct {
	key   string
	value *string
} {
	return []struct {
		key   string
		value *string
	}{
		{syncPrefix + "endpoint", &s.Endpoint},
		{syncPrefix + "bucket", &s.Bucket},
		{syncPrefix + "key", &s.Key},
		{syncPrefix + "region", &s.Region},
		{syncPrefix + "access_key_id", &s.AccessKeyID},
		{syncPrefix + "secret_access_key", &s.SecretAccessKey},
		{syncPrefix + "session_token", &s.SessionToken},
	}
}

func (q queries) config(ctx context.Context, key string) (string, error) {
	var value string
	err := q.q.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if err = wrap(err); errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get config %s: %w", key, err)
	}
	return value, nil
}

func (q queries) setConfig(ctx context.Context, key, value string) error {
	var err error
	if value == "" {
		_, err = q.q.ExecContext(ctx, `DELETE FROM config WHERE key = ?`, key)
	} else {
		_, err = q.q.ExecContext(ctx, `INSERT INTO config(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	}
	if err != nil {
		return fmt.Errorf("set config %s: %w", key, wrap(err))
	}
	return nil
}

func (q queries) configWithPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT key, value FROM config WHERE substr(key, 1, length(?)) = ?`, prefix, prefix)
	if err != nil {
		return nil, fmt.Errorf("list config %s: %w", prefix, wrap(err))
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("list config %s: %w", prefix, wrap(err))
		}
		values[strings.TrimPrefix(key, prefix)] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list config %s: %w", prefix, wrap(err))
	}
	return values, nil
}

func (q queries) GitHubToken(ctx context.Context) (string, error) {
	return q.config(ctx, githubTokenKey)
}

func (q queries) SetGitHubToken(ctx context.Context, token string) error {
	return q.setConfig(ctx, githubTokenKey, token)
}

func (q queries) WSLDistro(ctx context.Context) (string, error) {
	return q.config(ctx, wslDistroKey)
}

func (q queries) SetWSLDistro(ctx context.Context, distro string) error {
	return q.setConfig(ctx, wslDistroKey, distro)
}

func (q queries) SyncSettings(ctx context.Context) (SyncSettings, error) {
	values, err := q.configWithPrefix(ctx, syncPrefix)
	if err != nil {
		return SyncSettings{}, err
	}
	var settings SyncSettings
	for _, field := range settings.fields() {
		*field.value = values[strings.TrimPrefix(field.key, syncPrefix)]
	}
	return settings, nil
}

func (s *Store) SetSyncSettings(ctx context.Context, settings SyncSettings) error {
	return s.InTx(ctx, func(tx *Tx) error {
		for _, field := range settings.fields() {
			if err := tx.setConfig(ctx, field.key, *field.value); err != nil {
				return err
			}
		}
		return nil
	})
}

func (q queries) AppSettings(ctx context.Context) (map[string]string, error) {
	return q.configWithPrefix(ctx, appPrefix)
}

func (s *Store) SetAppSettings(ctx context.Context, settings map[string]string) error {
	return s.InTx(ctx, func(tx *Tx) error {
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM config WHERE substr(key, 1, length(?)) = ?`, appPrefix, appPrefix); err != nil {
			return fmt.Errorf("clear app settings: %w", wrap(err))
		}
		for name, value := range settings {
			if err := tx.setConfig(ctx, appPrefix+name, value); err != nil {
				return err
			}
		}
		return nil
	})
}
