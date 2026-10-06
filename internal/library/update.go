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
	validator := ""
	snapshot, err := openSnapshot(ctx, skill.Source)
	switch {
	case err != nil:
	case snapshot != nil:
		skill, changed, err = readSnapshotUpdate(ctx, snapshot, skill)
	case skill.Source.Type == SourceGitHub:
		skill, changed, err = l.readGitHubUpdate(ctx, skill, state.confirmedCommit)
	default:
		var latest Skill
		latest, validator, err = l.readUpdate(ctx, skill, state.httpValidator)
		if err == nil {
			changed = !skillfile.Equal(skill.Files, latest.Files)
			skill = latest
		}
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
	record := sourceGitRecord(skill.Source, skill.GitTreeSHA)
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
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sources SET last_fetched = ?, last_seen_ref = ?, http_validator = ? WHERE id = ?`,
		skill.Source.LastFetched.Format(time.RFC3339Nano), skill.Source.LastSeenRef, validator, skill.Source.ID); err != nil {
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

func readSnapshotUpdate(ctx context.Context, snapshot source.Snapshot, skill Skill) (Skill, bool, error) {
	changed, err := snapshot.Check(ctx, source.Have{Name: skill.Name, Files: skill.Files})
	if err != nil {
		return Skill{}, false, err
	}
	if changed {
		latest, err := snapshot.Fetch(ctx, []string{skill.Name})
		if err != nil {
			return Skill{}, false, err
		}
		skill.Files, skill.Description, skill.GitTreeSHA = latest[0].Files, latest[0].Description, ""
	}
	skill.Source.LastSeenRef = snapshot.Revision()
	skill.Source.LastFetched = time.Now().UTC()
	return skill, changed, nil
}

func (l *sqliteLibrary) readUpdate(ctx context.Context, skill Skill, validator string) (Skill, string, error) {
	discovered, err := l.fetchSource(ctx, skill.Source, "", validator)
	if err != nil {
		return Skill{}, "", err
	}
	skill.Source.LastFetched = time.Now().UTC()
	if discovered.unchanged {
		return skill, validator, nil
	}
	fetched, err := selectSkills(discovered.skills, []string{skill.Name})
	if err != nil {
		return Skill{}, "", err
	}
	skill.Files = fetched[0].files
	skill.GitTreeSHA = fetched[0].gitTreeSHA
	skill.Description = fetched[0].candidate.Description
	skill.Source.LastSeenRef = discovered.ref
	return skill, discovered.httpValidator, nil
}
