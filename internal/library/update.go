package library

import (
	"context"
	"errors"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// Update 重新读取来源,在其中按名字重新定位该 skill,不经入库预览。GitHub 先解析一次 commit:与当前版本记录的确认 commit 相同即无变化,不取 tree;
// 否则发现候选、比较该 Skill 目录的 tree SHA:相同则不取辅助文件、不新增版本,不同才取文件并写入新版本
// (带新 SHA 与本次 commit)。无变化时刷新当前版本的确认 commit 与 last_fetched / last_seen_ref;
// 无 SHA 的旧 GitHub 记录回退为比较文件与可执行位,成功后补写 SHA 与确认 commit。
// URL 在当前版本记录了 HTTP validator 时带条件头:304 即无变化,不下载、不新增版本,只刷新 last_fetched;
// 200 则整包比较文件,把新 validator 记到新版本或当前版本上,并写回 last_seen_ref。本地目录重新发现候选后比较文件。
// 只更新 Library,不重新安装已安装的文件夹。
// 联网期间不持库锁;提交前复核:skill 已被移除返回 NotFound,当前版本已被别处改写返回 Conflict
// (Details.SkillID),两者都不留下任何写入。
// 成功后已有 LastUpdateCheck 改为 current 并刷新确认时间;失败保留原记录,从未检查的仍为 nil。
func (l *Library) Update(ctx context.Context, id SkillID) (result UpdateResult, err error) {
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
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := requireCurrentVersion(ctx, tx, id, state.versionID); err != nil {
			return err
		}
		if changed {
			createdAt := clockNow()
			versionID, err := tx.AddVersion(ctx, int64(id), store.NewVersion{Description: skill.Description, Files: skill.Files, State: string(latest.State), CreatedAt: createdAt})
			if err != nil {
				return err
			}
			skill.VersionID = VersionID(versionID)
			skill.UpdatedAt = createdAt
		} else if err := tx.ConfirmVersion(ctx, int64(id), string(latest.State)); err != nil {
			return err
		}
		if err := tx.RecordSourceFetch(ctx, int64(skill.Source.ID), skill.Source.LastFetched, skill.Source.LastSeenRef); err != nil {
			return err
		}
		return tx.ConfirmUpdateCheck(ctx, int64(id), clockNow(), string(UpdateCurrent))
	})
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Skill: skill, Changed: changed}, nil
}

func requireCurrentVersion(ctx context.Context, tx *store.Tx, id SkillID, expected int64) error {
	actual, err := tx.CurrentVersionID(ctx, int64(id))
	if errors.Is(err, store.ErrNotFound) {
		return &Error{Code: NotFound, Message: "skill was removed while fetching", Details: ErrorDetails{SkillID: id}, Err: err}
	}
	if err != nil {
		return err
	}
	if actual != expected {
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
