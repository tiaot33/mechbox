package library

import (
	"context"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// AttachSourceRequest 指定给库里一个没有来源的技能接上哪个来源。
type AttachSourceRequest struct {
	SkillID SkillID `json:"skill_id"`
	// SourceInput 是来源,格式与识别规则同 PreviewFetchRequest.Input;界面预填 npx skills 的来源记录认出的来源
	// (SkillScopeInspection.SourceInput,或显示在这个技能下的未纳管技能的 UnmanagedSkill.SourceInput)。来源里按技能名找这个技能。
	SourceInput string `json:"source_input"`
	// SourceRef 是来源的分支或 tag,同 PreviewFetchRequest.Ref,只对 Git 类来源有意义;预填同上的 SourceRef。
	SourceRef string `json:"source_ref,omitempty"`
}

// AttachSource 给库里一个没有来源的技能接上来源:之后它和从这个来源入库的技能一样,进这个来源的集合(库里已有这个来源时进已有的集合),
// 照常检查更新、更新与回滚。技能 ID、版本历史、各处安装、组与备注都不变,不改磁盘,也不新增版本。
// 先按入库预览的规则识别并读取来源(联网期间不持库锁),在来源里按技能名取这个技能。这个来源还没有集合时,和入库一样按整个来源
// 记下当时有哪些技能,都算用户见过;已有集合时只把这个技能记为见过,集合上原来的新技能照旧提示。
// 当前版本和来源里的是什么关系(不比可执行位),按先后:
//   - 和来源里这个技能现在的内容一致:就是来源里的那一版(带来源版本,Skill.Revision),不再算用户的修改,检查更新照常。
//   - npx skills 的来源记录(各 Agent 目录里这个技能的文件夹与同名文件夹的,见 UnmanagedSkill.SourceInput)里有记着同一个来源、
//     哈希又对得上当前版本的:是当初从这个来源装的原样,作者的旧版,来源版本未知,检查更新按内容比较,不算用户的修改。
//   - 当前版本是用目录里的修改更新进来的(SkillVersion.BasedOn 不为空):照旧算这份修改,检查更新只看来源里的和它所基于的那一版相比有没有改过。
//   - 其余:说不清有没有用户的修改,标为 SkillVersion.MaybeEdited,检查更新按内容比较,更新计划提示会换掉修改。
//
// 错误:SourceInput 为空返回 InvalidArgument(Details.Field 为 source_input),来源识别不了时 Details.Field 为 source_input / source_ref;
// 技能不存在返回 NotFound;技能已经有来源返回 FailedPrecondition;来源里没有这个技能返回 NotFound(Details.Name 为技能名);
// 这个来源的集合里已经有同名的技能返回 AlreadyExists(Details.SkillID 为那个技能,Details.Name 为技能名);读来源期间库被拉回替换返回 Conflict。
// 来源连不上、读不了等同入库预览。失败时库不变。
func (l *Library) AttachSource(ctx context.Context, req AttachSourceRequest) (skill Skill, err error) {
	defer func() { err = typedError(err) }()
	const operation = "attach source"
	input := strings.TrimSpace(req.SourceInput)
	if input == "" {
		return Skill{}, argumentError(operation, "source_input", "a source input is required")
	}
	name, replica, err := l.sourcelessSkill(ctx, operation, req.SkillID)
	if err != nil {
		return Skill{}, err
	}
	opened, err := l.openSource(ctx, input, req.SourceRef)
	if err != nil {
		return Skill{}, err
	}
	sourced, err := opened.skill(ctx, name)
	if err != nil {
		return Skill{}, err
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	records, err := l.sourceRecordsOf(ctx, req.SkillID, name, input, req.SourceRef)
	if err != nil {
		return Skill{}, err
	}
	var row store.Skill
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.RequireReplica(ctx, replica); err != nil {
			return err
		}
		if current, err := tx.GetSkill(ctx, int64(req.SkillID)); err != nil {
			return err
		} else if current.Source.ID != 0 || current.Name != name {
			return preconditionError(operation, "the skill changed while the source was read", ErrorDetails{SkillID: req.SkillID})
		}
		followed, err := tx.FollowedSource(ctx, sourced.record.Type, sourced.record.Locator)
		if err != nil {
			return err
		}
		sourceID, err := joinCollection(ctx, tx, operation, sourced, name, followed == 0)
		if err != nil {
			return err
		}
		if err := tx.AttachSource(ctx, int64(req.SkillID), sourceID); err != nil {
			return err
		}
		current, err := tx.GetCurrent(ctx, int64(req.SkillID))
		if err != nil {
			return err
		}
		switch {
		case skillfile.SameContentFiles(current.Files, sourced.skill.Files):
			err = tx.MarkSourceVersion(ctx, int64(req.SkillID), string(sourced.skill.State), sourced.revision)
		case slices.ContainsFunc(records, func(record sourceRecord) bool { return record.Matches(current.Files) }):
			err = tx.MarkSourceVersion(ctx, int64(req.SkillID), "", "")
		case current.Base == 0:
			err = tx.MarkMaybeEdited(ctx, int64(req.SkillID))
		}
		if err != nil {
			return err
		}
		row, err = tx.GetSkill(ctx, int64(req.SkillID))
		return err
	})
	if err != nil {
		return Skill{}, err
	}
	return l.skillOf(row)
}

func (l *Library) sourcelessSkill(ctx context.Context, operation string, id SkillID) (string, string, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	row, err := l.store.GetSkill(ctx, int64(id))
	if err != nil {
		return "", "", err
	}
	if row.Source.ID != 0 {
		return "", "", preconditionError(operation, "the skill already has a source", ErrorDetails{SkillID: id})
	}
	replica, err := l.store.Replica(ctx)
	return row.Name, replica, err
}

func (l *Library) sourceRecordsOf(ctx context.Context, id SkillID, name, input, ref string) ([]sourceRecord, error) {
	folders, err := l.readableScopeFolders(ctx)
	if err != nil {
		return nil, err
	}
	records := l.sourceRecords()
	var found []sourceRecord
	for _, folder := range folders {
		if folder.skillID != id && (folder.skillID != 0 || skillName(folder.Folder) != name) {
			continue
		}
		if record, ok := records.find(ctx, folder.target, name, folder.Name); ok && record.from(input, ref) {
			found = append(found, record)
		}
	}
	return found, nil
}
