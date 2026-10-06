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
	changed := false
	var latest source.Skill
	snapshot, err := openSnapshot(ctx, skill.Source, state.snapshot)
	switch {
	case err != nil:
	case snapshot != nil:
		changed, latest, err = checkSnapshot(ctx, snapshot, source.Have{Name: skill.Name, Files: skill.Files, State: state.snapshot}, true)
		if changed {
			skill.Files, skill.Description, skill.GitTreeSHA = latest.Files, latest.Description, ""
		}
		if revision := snapshot.Revision(); revision != "" {
			skill.Source.LastSeenRef = revision
		}
		skill.Source.LastFetched = time.Now().UTC()
	default:
		skill, changed, err = l.readGitHubUpdate(ctx, skill, state.confirmedCommit)
	}
	if err != nil {
		return UpdateResult{}, err
	}
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
	record := sourceVersionRecord(skill.Source, skill.GitTreeSHA, latest.State)
	if changed {
		createdAt := clockNow()
		versionID, err := createVersion(ctx, tx, int64(id), skill.Files, record, skill.Description, createdAt)
		if err != nil {
			return UpdateResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, id); err != nil {
			return UpdateResult{}, fmt.Errorf("select current version: %w", err)
		}
		skill.UpdatedAt = createdAt
	} else if skill.Source.Type == SourceGitHub {
		if _, err := tx.ExecContext(ctx, `
			UPDATE skill_versions SET git_tree_sha = ?, confirmed_commit = ?
			WHERE id = (SELECT current_version_id FROM skills WHERE id = ?)
		`, record.treeSHA, record.confirmedCommit, id); err != nil {
			return UpdateResult{}, fmt.Errorf("confirm current version: %w", err)
		}
	} else if snapshot != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE skill_versions SET source_state = ?
			WHERE id = (SELECT current_version_id FROM skills WHERE id = ?)
		`, string(record.state), id); err != nil {
			return UpdateResult{}, fmt.Errorf("confirm current version: %w", err)
		}
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

func (l *sqliteLibrary) readGitHubUpdate(ctx context.Context, skill Skill, confirmedCommit string) (Skill, bool, error) {
	snap, err := l.readGitHubSnapshot(ctx, skill.Source)
	if err != nil {
		return Skill{}, false, err
	}
	snap.rememberGitBlobs(skill.Files)
	changed := false
	if !snap.confirms(confirmedCommit) {
		remote, err := snap.locate(ctx, skill)
		if err != nil {
			return Skill{}, false, err
		}
		persistedSHA := skill.GitTreeSHA
		shaKnown := persistedSHA != "" && remote.gitTreeSHA != ""
		changed = shaKnown && persistedSHA != remote.gitTreeSHA
		if !shaKnown || changed {
			files, err := snap.files(ctx, skill, remote)
			if err != nil {
				return Skill{}, false, err
			}
			if !shaKnown {
				changed = !skillfile.Equal(skill.Files, files)
			}
			skill.Files = files
		}
		skill.GitTreeSHA = remote.gitTreeSHA
		skill.Description = remote.description
	}
	skill.Source.LastSeenRef = snap.ref
	skill.Source.LastFetched = time.Now().UTC()
	return skill, changed, nil
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
