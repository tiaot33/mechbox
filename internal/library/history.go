package library

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// UpdateRecordID 是集合历史里一条记录的身份:一次更新、跳过或回滚。回滚、单独重试没换上的地方都按它引用那次更新。
type UpdateRecordID int64

// UpdateRecordKind 是集合历史里一条记录的种类。
type UpdateRecordKind string

const (
	UpdateRecordUpdate   UpdateRecordKind = "update"   // 一次更新(Update)
	UpdateRecordSkip     UpdateRecordKind = "skip"     // 一次跳过(SkipUpdate)
	UpdateRecordRollback UpdateRecordKind = "rollback" // 一次回滚(Rollback)
)

const (
	recordUpdate   = string(UpdateRecordUpdate)
	recordSkip     = string(UpdateRecordSkip)
	recordRollback = string(UpdateRecordRollback)
)

// UpdateRecord 是集合历史里的一条记录。记录里的地方只记是哪个 Agent、全局还是哪个项目,不记磁盘路径,
// 同步到别的机器也对得上;技能从库里移除后,记录照样留着。
type UpdateRecord struct {
	ID           UpdateRecordID   `json:"id"`
	CollectionID CollectionID     `json:"collection_id"`
	Kind         UpdateRecordKind `json:"kind"`
	// Revision 是当时来源的来源版本:更新换到的、跳过时更新计划看到的;回滚记被回滚那次更新的。本地目录来源为空。
	Revision  string    `json:"revision,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Commits 是更新时这段时间的提交说明,最新的在前;只有 GitHub 来源有。
	Commits []Commit `json:"commits,omitempty"`
	// WholeRevision 为 true 表示这次跳过的是整个来源版本。
	WholeRevision bool `json:"whole_revision,omitzero"`
	// Skills 是这条记录涉及的技能,按 ID 升序:更新是换了版本的技能,回滚是换回的技能,跳过是跳过了改动的技能(两个版本相同)。
	Skills []RecordedSkill `json:"skills"`
	// Installs 是更新或回滚时逐处重新安装的结果;更新之后单独重试的地方也记在那次更新上,覆盖原来的结果。
	Installs []RecordedInstall `json:"installs,omitempty"`
	// RolledBackBy 是回滚了这次更新的那条记录;没被回滚为零值。
	RolledBackBy UpdateRecordID `json:"rolled_back_by,omitzero"`
	// RollsBack 是这次回滚回滚的那次更新;其他记录为零值。
	RollsBack UpdateRecordID `json:"rolls_back,omitzero"`
}

// RecordedSkill 是一条记录里的一个技能:从哪个版本换到了哪个版本。技能已从库里移除时版本也不在了,Name 仍可展示。
type RecordedSkill struct {
	SkillID SkillID `json:"skill_id"`
	Name    string  `json:"name"` // 当时的技能名;换了名字时是之后的名字
	// FromName 是这次换了名字时之前的名字:确认改名的更新、回滚这样的更新;没换名字为空。
	FromName      string    `json:"from_name,omitempty"`
	FromVersionID VersionID `json:"from_version_id"` // 之前的版本
	ToVersionID   VersionID `json:"to_version_id"`   // 之后的版本
}

// RecordedInstall 是一条记录里一处重新安装的结果。
type RecordedInstall struct {
	SkillID SkillID `json:"skill_id"`
	Scope   Scope   `json:"scope"`
	// Outcome 在更新记录里是 ReinstallOutcome 的值,在回滚记录里是 RollbackOutcome 的值。
	Outcome string `json:"outcome"`
	// Error 是没换上(failed)时的错误码。
	Error ErrorCode `json:"error,omitempty"`
}

// UpdateHistoryFilter 缩小集合历史的范围,两个条件都可选、可组合:CollectionID 只看这个集合的,SkillID 只看涉及这个技能的
// (哪几次更新改过它、跳过或回滚过它)。都不给时是全库的历史。
type UpdateHistoryFilter struct {
	CollectionID CollectionID `json:"collection_id,omitzero"`
	SkillID      SkillID      `json:"skill_id,omitzero"`
}

// ListUpdateHistory 按时间倒序列出集合的历史:每次更新、跳过、回滚各一条。按技能筛选时技能不存在返回 NotFound;
// 集合里的技能都移除后,按集合 ID 仍能查到它的历史。
func (l *Library) ListUpdateHistory(ctx context.Context, filter UpdateHistoryFilter) (records []UpdateRecord, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if filter.SkillID != 0 {
		if _, err := l.store.GetSkill(ctx, int64(filter.SkillID)); err != nil {
			return nil, err
		}
	}
	rows, err := l.store.ListRecords(ctx, int64(filter.CollectionID), int64(filter.SkillID))
	if err != nil {
		return nil, err
	}
	records = []UpdateRecord{}
	for _, row := range rows {
		record, err := recordOf(row)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func recordOf(row store.Record) (UpdateRecord, error) {
	record := UpdateRecord{
		ID:            UpdateRecordID(row.ID),
		CollectionID:  CollectionID(row.SourceID),
		Kind:          UpdateRecordKind(row.Kind),
		Revision:      row.Revision,
		CreatedAt:     row.At,
		WholeRevision: row.WholeRevision,
		Skills:        []RecordedSkill{},
		RolledBackBy:  UpdateRecordID(row.RolledBackBy),
		RollsBack:     UpdateRecordID(row.RollsBack),
	}
	if row.Commits != "" {
		if err := json.Unmarshal([]byte(row.Commits), &record.Commits); err != nil {
			return UpdateRecord{}, fmt.Errorf("decode commits: %w", err)
		}
	}
	for _, skill := range row.Skills {
		record.Skills = append(record.Skills, RecordedSkill{SkillID: SkillID(skill.SkillID), Name: skill.Name, FromName: skill.FromName, FromVersionID: VersionID(skill.From), ToVersionID: VersionID(skill.To)})
	}
	for _, install := range row.Installs {
		record.Installs = append(record.Installs, RecordedInstall{SkillID: SkillID(install.SkillID), Scope: scopeRowOf(install.Scope).scope(), Outcome: install.Outcome, Error: ErrorCode(install.Error)})
	}
	return record, nil
}

func (l *Library) recordWithReplica(ctx context.Context, id UpdateRecordID) (store.Record, string, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	record, err := l.store.GetRecord(ctx, int64(id))
	if err != nil {
		return store.Record{}, "", err
	}
	replica, err := l.store.Replica(ctx)
	return record, replica, err
}

// RollbackOutcome 是回滚时一处的结果。值以后可能增加,调用方遇到不认识的值按「没装回、需要用户查看」处理。
type RollbackOutcome string

const (
	RollbackReinstalled RollbackOutcome = "reinstalled" // 还是那次换上的版本,装回了那次更新之前的版本
	RollbackCurrent     RollbackOutcome = "current"     // 本来就是那次更新之前的版本,没动
	// RollbackReinstalledSince 是之后又被重新安装成了库里的别的版本,不动。
	RollbackReinstalledSince RollbackOutcome = "reinstalled_since"
	RollbackModified         RollbackOutcome = "modified"     // 用户改过,不动
	RollbackMissing          RollbackOutcome = "missing"      // 文件夹不在了,不动
	RollbackRootMissing      RollbackOutcome = "root_missing" // 项目不在这台机器上,或还没对应到本机路径,不动
	RollbackUninstalled      RollbackOutcome = "uninstalled"  // 技能已不装在这里(包括 Agent 或项目已不在库里),不动
	RollbackRemoved          RollbackOutcome = "removed"      // 技能已从库里移除,不动
	RollbackFailed           RollbackOutcome = "failed"       // 没装回,原因见 Error:权限或 I/O 故障;这一处保持原样
)

// RollbackInstall 是回滚时一处的结果。
type RollbackInstall struct {
	SkillID SkillID         `json:"skill_id"`
	Scope   Scope           `json:"scope"`
	Outcome RollbackOutcome `json:"outcome"`
	Error   *Error          `json:"error,omitempty"` // 仅 RollbackFailed 时非空
}

// RollbackResult 是一次回滚的结果。
type RollbackResult struct {
	Record UpdateRecordID `json:"record"` // 这次回滚的记录
	// Skills 是库里换回的技能:FromVersionID 是回滚前的当前版本,ToVersionID 是那次更新之前的版本。
	Skills []RecordedSkill `json:"skills"`
	// Removed 是那次更新涉及、但已从库里移除的技能,取自那次更新的记录;库里没有可换回的。
	Removed []RecordedSkill `json:"removed,omitempty"`
	// Installs 是那次更新装过的每一处(含单独重试换上的)现在的结果,按技能 ID、Agent 排序。
	Installs []RollbackInstall `json:"installs"`
}

// Rollback 回滚一次更新:那次更新换了版本的技能,库里一律换回那次之前的版本,之后又被更新过也一样,不要求先回滚之后那次。
// 那次更新装过的地方(含单独重试换上的)按磁盘上的文件内容核对:还是那次换上的版本就装回旧版;之后又被重新安装过、用户改过、
// 文件夹不在了、项目不在这台机器上或还没对应到本机路径、技能已不装在那里或已从库里移除的,都不动,结果里写明原因,不中断整个回滚。
// 回滚掉的改动自动记为跳过:不再计入角标,想要时在更新计划里照样能勾上再更新;回滚的是确认过的改名时,改名推测记为「不是」、
// 旧名字的技能记为继续保留,来源里那个新名字的内容又变了才再推测。回滚本身在集合的历史里记一条,
// 那次更新的 RolledBackBy 指向它。
//
// 错误:记录不存在返回 NotFound;不是更新记录返回 InvalidArgument(Details.Field 为 record);那次更新已被回滚、各处也都核对过了
// 返回 FailedPrecondition。库换回之后被取消时,返回已核对完的几处与 Canceled;再对同一次更新调用 Rollback 接着核对剩下的地方,
// 库不再动。
func (l *Library) Rollback(ctx context.Context, id UpdateRecordID) (result RollbackResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "rollback"
	record, err := l.store.GetRecord(ctx, int64(id))
	if err != nil {
		return RollbackResult{}, err
	}
	if record.Kind != recordUpdate {
		return RollbackResult{}, argumentError(operation, "record", "only an update can be rolled back")
	}
	result = RollbackResult{Skills: []RecordedSkill{}, Installs: []RollbackInstall{}}
	updated := map[SkillID]store.RecordSkill{}
	removed := map[SkillID]bool{}
	for _, skill := range record.Skills {
		updated[SkillID(skill.SkillID)] = skill
	}
	done := map[placeKey]bool{}
	if record.RolledBackBy != 0 {
		previous, err := l.store.GetRecord(ctx, record.RolledBackBy)
		if err != nil {
			return RollbackResult{}, err
		}
		result.Record = UpdateRecordID(previous.ID)
		rolledBack := map[SkillID]bool{}
		for _, skill := range previous.Skills {
			rolledBack[SkillID(skill.SkillID)] = true
			result.Skills = append(result.Skills, RecordedSkill{SkillID: SkillID(skill.SkillID), Name: skill.Name, FromName: skill.FromName, FromVersionID: VersionID(skill.From), ToVersionID: VersionID(skill.To)})
		}
		for _, skill := range record.Skills {
			if id := SkillID(skill.SkillID); !rolledBack[id] {
				removed[id] = true
				result.Removed = append(result.Removed, RecordedSkill{SkillID: id, Name: skill.Name, FromVersionID: VersionID(skill.From), ToVersionID: VersionID(skill.To)})
			}
		}
		for _, install := range previous.Installs {
			done[placeOf(install)] = true
			item := RollbackInstall{SkillID: SkillID(install.SkillID), Scope: scopeRowOf(install.Scope).scope(), Outcome: RollbackOutcome(install.Outcome)}
			if install.Error != "" {
				item.Error = &Error{Code: ErrorCode(install.Error), Message: operation + ": " + install.Error}
			}
			result.Installs = append(result.Installs, item)
		}
		pending := false
		for _, install := range record.Installs {
			if rolledBackPlace(install) && !done[placeOf(install)] {
				pending = true
			}
		}
		if !pending {
			return RollbackResult{}, preconditionError(operation, "the update was already rolled back", ErrorDetails{})
		}
		return l.rollbackPlaces(ctx, operation, record, result, updated, removed, done)
	}
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		now := clockNow()
		rollback := store.Record{SourceID: record.SourceID, Kind: recordRollback, Revision: record.Revision, RollsBack: record.ID, At: now}
		for _, skill := range record.Skills {
			id := SkillID(skill.SkillID)
			row, err := tx.GetSkill(ctx, skill.SkillID)
			if err == nil {
				err = tx.SetCurrentVersion(ctx, skill.SkillID, skill.From)
			}
			targetName := cmp.Or(skill.FromName, skill.Name)
			renamedBack := err == nil && targetName != row.Name
			if renamedBack {
				if err = tx.RenameSkill(ctx, skill.SkillID, targetName); errors.Is(err, store.ErrAlreadyExists) {
					return &Error{Code: Conflict, Message: operation + ": another skill in the collection already has the old name", Details: ErrorDetails{SkillID: id, Name: targetName}, Err: err}
				}
			}
			if errors.Is(err, store.ErrNotFound) {
				removed[id] = true
				result.Removed = append(result.Removed, RecordedSkill{SkillID: id, Name: skill.Name, FromVersionID: VersionID(skill.From), ToVersionID: VersionID(skill.To)})
				continue
			}
			if err != nil {
				return err
			}
			files, err := tx.VersionFiles(ctx, skill.To)
			if err != nil {
				return err
			}
			content := skillfile.Sum(files)
			name, fromName := row.Name, ""
			if renamedBack {
				name, fromName = targetName, row.Name
			}
			rollback.Skills = append(rollback.Skills, store.RecordSkill{SkillID: skill.SkillID, Name: name, FromName: fromName, From: row.VersionID, To: skill.From})
			rollback.Skipped = append(rollback.Skipped, store.SkippedChange{SkillID: skill.SkillID, Content: content})
			conclusion := store.UpdateCheck{At: now, Status: string(UpdateAvailable), Revision: record.Revision, Content: content, Found: true, Since: math.MaxInt64}
			if renamedBack {
				conclusion = store.UpdateCheck{At: now, Status: string(UpdateDeleted), Revision: record.Revision, Since: math.MaxInt64}
			}
			if err := tx.SaveUpdateCheck(ctx, skill.SkillID, conclusion); err != nil {
				return err
			}
			if renamedBack {
				renamed, err := tx.VersionFiles(ctx, row.VersionID)
				if err != nil {
					return err
				}
				if err := tx.RejectRename(ctx, skill.SkillID, row.Name, skillfile.Sum(renamed)); err != nil {
					return err
				}
				if err := tx.KeepDeleted(ctx, skill.SkillID); err != nil {
					return err
				}
				if err := tx.MarkSeen(ctx, record.SourceID, []string{row.Name}); err != nil {
					return err
				}
			}
			result.Skills = append(result.Skills, RecordedSkill{SkillID: id, Name: name, FromName: fromName, FromVersionID: VersionID(row.VersionID), ToVersionID: VersionID(skill.From)})
		}
		recordID, err := tx.AddRecord(ctx, rollback)
		result.Record = UpdateRecordID(recordID)
		return err
	})
	if err != nil {
		return RollbackResult{}, err
	}
	return l.rollbackPlaces(ctx, operation, record, result, updated, removed, done)
}

type placeKey struct {
	skillID int64
	agent   string
	project int64
}

func placeOf(install store.RecordInstall) placeKey {
	key := placeKey{skillID: install.SkillID, agent: install.Scope.Agent}
	if install.Scope.ProjectID != nil {
		key.project = *install.Scope.ProjectID
	}
	return key
}

func rolledBackPlace(install store.RecordInstall) bool {
	return install.Outcome == string(Reinstalled) || install.Outcome == string(ReinstallCurrent)
}

func (l *Library) rollbackPlaces(ctx context.Context, operation string, record store.Record, result RollbackResult, updated map[SkillID]store.RecordSkill, removed map[SkillID]bool, done map[placeKey]bool) (RollbackResult, error) {
	for _, install := range record.Installs {
		if !rolledBackPlace(install) || done[placeOf(install)] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		skillID := SkillID(install.SkillID)
		item := RollbackInstall{SkillID: skillID, Scope: scopeRowOf(install.Scope).scope()}
		if removed[skillID] {
			item.Outcome = RollbackRemoved
		} else {
			outcome, itemErr := l.rollbackInstall(ctx, operation, skillID, item.Scope, updated[skillID])
			if itemErr != nil && ctx.Err() != nil && errors.Is(itemErr, ctx.Err()) {
				return result, ctx.Err()
			}
			item.Outcome = outcome
			if itemErr != nil {
				item.Outcome, item.Error = RollbackFailed, ErrorOf(itemErr)
			}
		}
		recorded := store.RecordInstall{SkillID: install.SkillID, Scope: install.Scope, Outcome: string(item.Outcome)}
		if item.Error != nil {
			recorded.Error = string(item.Error.Code)
		}
		if err := l.store.InTx(ctx, func(tx *store.Tx) error { return tx.RecordInstall(ctx, int64(result.Record), recorded) }); err != nil {
			return result, err
		}
		result.Installs = append(result.Installs, item)
	}
	return result, nil
}

func (l *Library) rollbackInstall(ctx context.Context, operation string, id SkillID, scope Scope, update store.RecordSkill) (RollbackOutcome, error) {
	target, err := l.resolveScope(ctx, operation, scope)
	if err != nil && ErrorOf(err).Code == NotFound {
		return RollbackUninstalled, nil
	}
	if err != nil {
		return "", err
	}
	place, err := l.store.PlaceSkillScopes(ctx, int64(id), target.record())
	if err != nil {
		return "", err
	}
	if len(place) == 0 {
		return RollbackUninstalled, nil
	}
	if missing, err := target.rootMissing(operation); err != nil {
		return "", err
	} else if missing != nil {
		return RollbackRootMissing, nil
	}
	skill, current, err := l.getCurrent(ctx, id)
	if err != nil {
		return "", err
	}
	if current.versionID != update.From {
		return "", &Error{Code: Conflict, Message: operation + ": skill version changed since the rollback", Details: ErrorDetails{SkillID: id}}
	}
	folderName := place[0].Folder
	folder := filepath.Join(target.directory, folderName)
	if exists, err := agentdir.Exists(folder); err != nil {
		return "", fmt.Errorf("inspect target folder: %w", err)
	} else if !exists {
		return RollbackMissing, nil
	}
	disk, err := agentdir.Read(ctx, folder)
	if errors.Is(err, agentdir.ErrIrregularFile) {
		return RollbackModified, nil
	}
	if err != nil {
		return "", err
	}
	if maps.Equal(disk, digests(current.files)) && folderName == skill.Name {
		return RollbackCurrent, nil
	}
	updatedFiles, err := l.store.VersionDigests(ctx, update.To)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if err == nil && maps.Equal(disk, updatedFiles) || maps.Equal(disk, digests(current.files)) {
		if folderName != skill.Name {
			if err := l.requireFreeFolder(ctx, operation, skill, target); err != nil {
				return "", err
			}
		}
		ids := make([]int64, len(place))
		for i, row := range place {
			ids[i] = row.ID
		}
		record := func(tx *store.Tx) error { return tx.ResetFolders(ctx, ids) }
		if err := l.place(ctx, []*placement{{skill: skill, files: current.files, scope: target, replaces: folderName}}, record); err != nil {
			return "", err
		}
		return RollbackReinstalled, nil
	}
	status, _, err := l.inspectSkillScope(ctx, id, target)
	if err != nil {
		return "", err
	}
	if status == SkillScopeStale {
		return RollbackReinstalledSince, nil
	}
	return RollbackModified, nil
}
