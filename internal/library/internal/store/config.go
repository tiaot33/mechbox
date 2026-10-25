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
	aiPrefix       = "ai."
)

const machineConfigWhere = `substr(key, 1, length(?)) = ? OR key = ?`

var machineConfigArgs = []any{syncPrefix, syncPrefix, wslDistroKey}

type SyncSettings struct {
	Endpoint, Bucket, Folder, Region, AccessKeyID, SecretAccessKey, SessionToken, Keep, AutoPush string
}

func (s *SyncSettings) fields() []configField {
	return []configField{
		{"endpoint", &s.Endpoint},
		{"bucket", &s.Bucket},
		{"folder", &s.Folder},
		{"region", &s.Region},
		{"access_key_id", &s.AccessKeyID},
		{"secret_access_key", &s.SecretAccessKey},
		{"session_token", &s.SessionToken},
		{"keep", &s.Keep},
		{"auto_push", &s.AutoPush},
	}
}

type SyncState struct {
	Device, LastPushedAt, LastPushedTo string
}

func (s *SyncState) fields() []configField {
	return []configField{
		{"device", &s.Device},
		{"last_pushed_at", &s.LastPushedAt},
		{"last_pushed_to", &s.LastPushedTo},
	}
}

type AISettings struct {
	Provider, BaseURL, APIKey, Model, Headers, Body string
}

func (s *AISettings) fields() []configField {
	return []configField{
		{"provider", &s.Provider},
		{"base_url", &s.BaseURL},
		{"api_key", &s.APIKey},
		{"model", &s.Model},
		{"headers", &s.Headers},
		{"body", &s.Body},
	}
}

type configField struct {
	key   string
	value *string
}

func (q queries) readConfigFields(ctx context.Context, prefix string, fields []configField) error {
	values, err := q.configWithPrefix(ctx, prefix)
	if err != nil {
		return err
	}
	for _, field := range fields {
		*field.value = values[field.key]
	}
	return nil
}

func (s *Store) writeConfigFields(ctx context.Context, prefix string, fields []configField) error {
	return s.InTx(ctx, func(tx *Tx) error {
		for _, field := range fields {
			if err := tx.setConfig(ctx, prefix+field.key, *field.value); err != nil {
				return err
			}
		}
		return nil
	})
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
	var settings SyncSettings
	err := q.readConfigFields(ctx, syncPrefix, settings.fields())
	return settings, err
}

func (s *Store) SetSyncSettings(ctx context.Context, settings SyncSettings) error {
	return s.writeConfigFields(ctx, syncPrefix, settings.fields())
}

func (q queries) SyncState(ctx context.Context) (SyncState, error) {
	var state SyncState
	err := q.readConfigFields(ctx, syncPrefix, state.fields())
	return state, err
}

func (s *Store) SetSyncState(ctx context.Context, state SyncState) error {
	return s.writeConfigFields(ctx, syncPrefix, state.fields())
}

func (q queries) AISettings(ctx context.Context) (AISettings, error) {
	var settings AISettings
	err := q.readConfigFields(ctx, aiPrefix, settings.fields())
	return settings, err
}

func (s *Store) SetAISettings(ctx context.Context, settings AISettings) error {
	return s.writeConfigFields(ctx, aiPrefix, settings.fields())
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
