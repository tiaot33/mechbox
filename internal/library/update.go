package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (l *sqliteLibrary) Update(ctx context.Context, id SkillID) (result UpdateResult, err error) {
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return UpdateResult{}, &Error{Code: InvalidArgument, Message: "update: skill ID must be positive"}
	}
	// 网络前读出 skill 与来源读取记录;state.versionID 是提交阶段复核的依据。
	skill, state, err := l.getSkill(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	// 网络往返不持库锁:期间库可以被别的请求读写,提交前再复核。
	changed := false
	validator := "" // 本次读取后应记在 Source 上的 HTTP validator;GitHub / local 恒为空
	if skill.Source.Type == SourceGitHub {
		skill, changed, err = l.readGitHubUpdate(ctx, skill, state.confirmedCommit)
	} else {
		var latest Skill
		latest, validator, err = l.readUpdate(ctx, skill, state.httpValidator)
		if err == nil {
			changed = !filesEqual(skill.Files, latest.Files)
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
	// 任何写入之前先复核:skill 仍在、当前版本仍是网络前看到的那个;否则不留部分写入。
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
		// 无变化:当前版本的目录 tree 在本次 commit 上再次被确认为远端最新;无 SHA 的旧记录同时补写 SHA。
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
	// 成功更新也确认了当前版本;时间用于阻止旧检查回写,从未检查的技能仍保持空结论。
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET update_checked_at = ?, update_check_status = CASE WHEN update_check_status = '' THEN '' ELSE ? END WHERE id = ?`,
		formatStoredTime(clockNow()), UpdateCurrent, id); err != nil {
		return UpdateResult{}, fmt.Errorf("confirm update check: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, fmt.Errorf("commit update: %w", err)
	}
	return UpdateResult{Skill: skill, Changed: changed}, nil
}

// requireCurrentVersion 是提交阶段的复核:skill 已被移除 → NotFound;当前版本已被别处改写 → Conflict。
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

// readGitHubUpdate 先解析 commit:与当前版本的确认 commit 相同即无变化、不取 tree;否则发现候选后按目录 tree SHA
// 决定是否取完整文件,SHA 相同只读发现元数据。取文件时用库中内容按 git blob SHA 复用未变正文,只请求未匹配的 blob。
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
				changed = !filesEqual(skill.Files, files)
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

// readUpdate 读取目标 skill 的最新来源内容,在来源里按名字重新定位它,不持久化来源状态;GitHub 走 readGitHubUpdate。
// validator 是 Source 记录的 HTTP validator:URL / Store 据它发条件请求,304 表示远端未变——文件与 last_seen_ref
// 沿用库中的,只刷新 last_fetched。返回值里的 validator 是读取后应记在 Source 上的值(304 时保持不变)。
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
