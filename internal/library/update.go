package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
)

func (l *sqliteLibrary) Update(ctx context.Context, id SkillID) (result UpdateResult, err error) {
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return UpdateResult{}, &Error{Code: InvalidArgument, Message: "update: skill ID must be positive"}
	}
	skill, state, err := l.getSkill(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	snapshot, err := l.openSnapshot(ctx, skill.Source, "", state.snapshot)
	if err != nil {
		return UpdateResult{}, err
	}
	changed, latest, err := checkSnapshot(ctx, snapshot, source.Have{Name: skill.Name, Files: skill.Files, State: state.snapshot}, true)
	if err != nil {
		return UpdateResult{}, err
	}
	if changed {
		skill.Files, skill.Description = latest.Files, latest.Description
	}
	if revision := snapshot.Revision(); revision != "" {
		skill.Source.LastSeenRef = revision
	}
	skill.Source.LastFetched = time.Now().UTC()
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("begin update: %w", err)
	}
	defer tx.Rollback()
	if err := requireCurrentVersion(ctx, tx, id, state.versionID); err != nil {
		return UpdateResult{}, err
	}
	if changed {
		createdAt := clockNow()
		versionID, err := createVersion(ctx, tx, int64(id), skill.Files, latest.State, skill.Description, createdAt)
		if err != nil {
			return UpdateResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, id); err != nil {
			return UpdateResult{}, fmt.Errorf("select current version: %w", err)
		}
		skill.VersionID = VersionID(versionID)
		skill.UpdatedAt = createdAt
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE skill_versions SET source_state = ?
		WHERE id = (SELECT current_version_id FROM skills WHERE id = ?)
	`, string(latest.State), id); err != nil {
		return UpdateResult{}, fmt.Errorf("confirm current version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sources SET last_fetched = ?, last_seen_ref = ? WHERE id = ?`,
		skill.Source.LastFetched.Format(time.RFC3339Nano), skill.Source.LastSeenRef, skill.Source.ID); err != nil {
		return UpdateResult{}, fmt.Errorf("update source: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET update_checked_at = ?, update_check_status = CASE WHEN update_check_status = '' THEN '' ELSE ? END WHERE id = ?`,
		formatStoredTime(clockNow()), UpdateCurrent, id); err != nil {
		return UpdateResult{}, fmt.Errorf("confirm update check: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, fmt.Errorf("commit update: %w", err)
	}
	return UpdateResult{Skill: skill, Changed: changed}, nil
}

func requireCurrentVersion(ctx context.Context, tx *sql.Tx, id SkillID, expected int64) error {
	var actual sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT current_version_id FROM skills WHERE id = ?`, id).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: NotFound, Message: "skill was removed while fetching", Details: ErrorDetails{SkillID: id}, Err: err}
	}
	if err != nil {
		return fmt.Errorf("verify skill version: %w", err)
	}
	if actual.Int64 != expected {
		return &Error{Code: Conflict, Message: "skill changed while fetching", Details: ErrorDetails{SkillID: id}}
	}
	return nil
}

func checkSnapshot(ctx context.Context, snapshot source.Snapshot, have source.Have, needFiles bool) (bool, source.Skill, error) {
	status, state, err := snapshot.Check(ctx, have)
	if err != nil {
		return false, source.Skill{}, err
	}
	switch {
	case status == source.Unchanged:
		return false, source.Skill{Name: have.Name, Files: have.Files, State: state}, nil
	case status == source.Changed && !needFiles:
		return true, source.Skill{}, nil
	}
	fetched, err := snapshot.Fetch(ctx, []string{have.Name})
	if err != nil {
		return false, source.Skill{}, err
	}
	latest := fetched[0]
	return status == source.Changed || !skillfile.Equal(have.Files, latest.Files), latest, nil
}
