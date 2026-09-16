package library

import (
	"context"
	"fmt"
	"time"
)

func (l *sqliteLibrary) Update(ctx context.Context, id SkillID) (result UpdateResult, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return UpdateResult{}, &Error{Code: InvalidArgument, Message: "update: skill ID must be positive"}
	}
	skill, state, err := l.getSkill(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	changed := false
	validator := "" // 本次抓取后应记在 Source 上的 HTTP validator;GitHub / local 恒为空
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
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("begin update: %w", err)
	}
	defer tx.Rollback()
	record := sourceGitRecord(skill.Source, skill.GitTreeSHA)
	if changed {
		versionID, err := createVersion(ctx, tx, int64(id), skill.Files, record)
		if err != nil {
			return UpdateResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, id); err != nil {
			return UpdateResult{}, fmt.Errorf("select current version: %w", err)
		}
	} else if skill.Source.Type == SourceGitHub {
		// 无变化:当前版本的目录 tree 在本次 commit 上再次被确认为远端最新;无 SHA 的旧记录同时补写 SHA。
		if _, err := tx.ExecContext(ctx, `
			UPDATE skill_versions SET git_tree_sha = ?, confirmed_commit = ?
			WHERE id = (SELECT current_version_id FROM skills WHERE id = ?)
		`, record.treeSHA, record.confirmedCommit, id); err != nil {
			return UpdateResult{}, fmt.Errorf("confirm current version: %w", err)
		}
	}
	if skill.Source.Type != SourceLocal {
		if _, err := tx.ExecContext(ctx, `UPDATE sources SET last_fetched = ?, last_seen_ref = ?, http_validator = ? WHERE id = ?`,
			skill.Source.LastFetched.Format(time.RFC3339Nano), skill.Source.LastSeenRef, validator, skill.Source.ID); err != nil {
			return UpdateResult{}, fmt.Errorf("update source: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, fmt.Errorf("commit update: %w", err)
	}
	return UpdateResult{Skill: skill, Changed: changed, Note: "已激活的副本未重写,可能落后于 Library;需要时请重新激活。"}, nil
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
	}
	skill.Source.LastSeenRef = snap.ref
	skill.Source.LastFetched = time.Now().UTC()
	return skill, changed, nil
}

// readUpdate 读取目标 skill 的最新来源内容,不持久化来源状态;GitHub 走 readGitHubUpdate。
// validator 是 Source 记录的 HTTP validator:URL / Store 据它发条件请求,304 表示远端未变——文件与 last_seen_ref
// 沿用库中的,只刷新 last_fetched。返回值里的 validator 是抓取后应记在 Source 上的值(304 时保持不变)。
func (l *sqliteLibrary) readUpdate(ctx context.Context, skill Skill, validator string) (Skill, string, error) {
	if skill.Source.Type == SourceLocal {
		files, err := readFiles(ctx, skill.Source.Locator.Path)
		if err != nil {
			return Skill{}, "", err
		}
		skill.Files = files
		return skill, "", nil
	}
	discovered, err := l.fetchRemote(ctx, skill.Source, "", validator)
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
	skill.Source.LastSeenRef = discovered.ref
	return skill, discovered.httpValidator, nil
}

func (l *sqliteLibrary) CheckUpdate(ctx context.Context, id SkillID) (available bool, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return false, &Error{Code: InvalidArgument, Message: "check update: skill ID must be positive"}
	}
	skill, state, err := l.getSkill(ctx, id)
	if err != nil {
		return false, err
	}
	if skill.Source.Type == SourceGitHub {
		snap, err := l.readGitHubSnapshot(ctx, skill.Source)
		if err != nil {
			return false, err
		}
		return githubSkillHasUpdate(ctx, &snap, skill, state.confirmedCommit)
	}
	// URL / Store 带条件头:304 时 readUpdate 原样返回库中文件,自然判定无更新。
	latest, _, err := l.readUpdate(ctx, skill, state.httpValidator)
	if err != nil {
		return false, err
	}
	return !filesEqual(skill.Files, latest.Files), nil
}
