package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// UpdateCheckStatus 是检查更新的结论,随技能保存在 Skill.LastUpdateCheck 上。
type UpdateCheckStatus string

const (
	UpdateAvailable UpdateCheckStatus = "available" // 有新版本
	UpdateCurrent   UpdateCheckStatus = "current"   // 已是最新;更新成功后也是它
	// UpdateDeleted 是「来源里已删除」:来源读到了,但里面已经没有这个名字的技能(作者删掉或改了名)。
	// 库里的技能和装出去的文件夹都不动,要不要移除由用户决定。来源整个读不到时不算这种,算 UpdateFailed;
	// 来源里还有这个技能但读不全(文件取不到、digest 不一致、SKILL.md 缺 name / description、索引里这一条坏了)也算 UpdateFailed。
	UpdateDeleted UpdateCheckStatus = "deleted"
	UpdateFailed  UpdateCheckStatus = "failed" // 检查失败:这次没读成来源,可重试;原因只在当场的 CollectionUpdateCheck.Error 里,不保存
)

// UpdateCheck 是随技能保存的检查更新结论,重启后仍可读到。
type UpdateCheck struct {
	CheckedAt time.Time         `json:"checked_at"` // 最近一次确认来源的时间:检查更新、更新计划、更新或回滚
	Status    UpdateCheckStatus `json:"status"`
	// Revision 是这次确认来源时看到的来源版本,与入库预览的 Revision 同义:Git 类来源为 commit,直接下载为文件内容哈希,well-known 为索引内容哈希;
	// 本地目录来源与检查失败时为空。
	Revision string `json:"revision,omitempty"`
	// Skipped 为 true 表示有新版本(或推测出了改名),但来源里这份内容用户之前跳过了(SkipUpdate)。来源里这个技能的内容又变了,
	// 下次检查更新后它回到 false;同一来源里别的技能变了不影响。
	Skipped bool `json:"skipped,omitzero"`
	// RenamedTo 是结论为来源里已删除时,推测这个技能在来源里改名成的新名字:同一来源里它消失了,同时出现了一个用户没见过、
	// 内容大部分相同的技能。用户在更新计划里确认(勾上更新)、否认(RejectRename)或跳过(SkipUpdate);没有推测时为空。
	RenamedTo string `json:"renamed_to,omitempty"`
	// Pending 为 true 表示这个技能计入角标,侧边栏的更新计数与 Dock 角标都按它算:有新版本且没跳过,
	// 或来源里已删除且用户没选继续保留(Skill.KeptDeleted),或推测出了改名(RenamedTo)且没跳过。本地目录来源的技能、跳过的改动、
	// 来源里的新技能都不计入。
	Pending bool `json:"pending,omitzero"`
}

// CollectionUpdateCheck 是检查更新时一个集合的结果。
type CollectionUpdateCheck struct {
	CollectionID CollectionID `json:"collection_id"`
	Source       Source       `json:"source,omitzero"` // 集合来自哪里;集合不存在时为零值
	// Skills 是检查后集合里的技能,按 ID 升序;各自的 LastUpdateCheck 就是这次的结论,
	// 检查期间别处写下了较新的结论时给出较新的。检查期间被移除的技能不出现。
	Skills []Skill `json:"skills,omitempty"`
	// Error 是这次没读成来源的原因(连不上、没权限、分支已不存在、来源内容不可用),结论为 failed 的技能就是因此失败。
	// 集合不存在时只有它(NotFound),集合 ID 不是正数时为 InvalidArgument。
	Error *Error `json:"error,omitempty"`
}

// CheckUpdates 检查给定集合有没有新版本;整库检查就是把 ListCollections 的集合 ID 全部传入。
// 重复的集合只检查一次,结果按传入顺序每个集合一项。没有按技能、按组检查的入口,技能详情页的「有新版本」读 Skill.LastUpdateCheck。
//
// 检查只读:不改技能内容和版本,不碰装出去的文件夹,可以在用户没有操作时发起。集合里的技能都按来源的同一份内容判断,
// 结论写在集合里每个技能的 LastUpdateCheck 上,重启后仍可读到:有新版本、已是最新、来源里已删除,
// 或没读成来源时的检查失败(原因在 CollectionUpdateCheck.Error)。结论还带上看到的来源版本、这次的改动是否跳过过,
// 以及是否计入角标(见 UpdateCheck)。来源换了来源版本时,同时按整个来源记下新技能,列在 Collection.NewSkills 上。
// 当前版本是用目录里的修改更新进来的技能(PlannedSkill.Edited),只看来源里这个技能和这份修改所基于的来源版本相比有没有改过:
// 没改过,或改成了和这份修改一样,都是已是最新。本地目录来源的作者就是用户自己,照旧拿目录里的内容和库里的当前版本比较。
// 从作用域连同来源入库、和来源里的不一样的那一版(来源版本未知,含 SkillVersion.MaybeEdited)没有所基于的来源版本,按内容和来源比较。
// 不同集合互不影响,一个来源连不上不妨碍其他集合。
//
// 同时有几次检查、或检查期间技能被更新时,结论以较新的为准,不报错:检查开始后别处写下了结论(检查、更新计划、更新、继续保留)时
// 不覆盖它,结果里直接给出它;只有别处写下的检查失败会被这次的结论覆盖。
//
// 集合不存在或 ID 不是正数只放进该集合的 Error。取消时返回已检查完的集合和 Canceled,没检查完的集合保留原来的结论;
// 写库失败作为整体错误返回,连同已检查完的集合。
func (l *Library) CheckUpdates(ctx context.Context, collections []CollectionID) (results []CollectionUpdateCheck, err error) {
	defer func() { err = typedError(err) }()
	results = []CollectionUpdateCheck{}
	seen := make(map[CollectionID]bool, len(collections))
	for _, id := range collections {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		result, err := l.checkCollection(ctx, id)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (l *Library) checkCollection(ctx context.Context, id CollectionID) (CollectionUpdateCheck, error) {
	result := CollectionUpdateCheck{CollectionID: id}
	if id <= 0 {
		result.Error = argumentError("check updates", "collections", "collection ID must be positive")
		return result, nil
	}
	read, err := l.readCollection(ctx, id)
	if err != nil {
		return result, err
	}
	if read == nil {
		result.Error = &Error{Code: NotFound, Message: "check updates: collection not found"}
		return result, nil
	}
	result.Source = read.skills[0].Source
	conclusions, failure := read.conclusions()
	result.Error = failure
	result.Skills, err = l.saveCollectionRead(ctx, read, conclusions)
	if errors.Is(err, store.ErrReplaced) {
		result.Skills, result.Error = nil, ErrorOf(err)
		return result, nil
	}
	return result, err
}

type collectionRead struct {
	skills   []Skill
	states   []currentVersion
	replica  string
	listedAt store.Listed
	snapshot source.Snapshot
	openErr  error
	checked  []checkedSkill
	revision string
	listed   []string
	rejected []map[string][]string
	linked   []store.RolledBackRename
	seen     map[string]bool
	renames  map[int]renameGuess
}

type renameGuess struct {
	latest  source.Skill
	content string
}

func (l *Library) readCollection(ctx context.Context, id CollectionID) (*collectionRead, error) {
	read, err := l.collectionCurrent(ctx, id)
	if err != nil || read == nil {
		return nil, err
	}
	read.checked = make([]checkedSkill, len(read.skills))
	first := read.states[0]
	read.snapshot, read.openErr = l.sources.Open(ctx, first.source.Type, first.source.Locator, "", first.origin.State)
	if read.openErr != nil {
		for i := range read.checked {
			read.checked[i].err = read.openErr
		}
	} else {
		for {
			read.checked = checkSkills(ctx, read.snapshot, haves(read.states), true)
			keepEdits(read.checked, read.states)
			read.revision = read.snapshot.Revision()
			read.listed = nil
			if read.revision == "" || read.revision != read.listedAt.Revision {
				if names, err := read.snapshot.Names(ctx); err == nil {
					read.listed = names
				}
			}
			read.renames = guessRenames(ctx, read)
			if read.snapshot.Revision() == read.revision {
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

func (r *collectionRead) conclusions() ([]store.UpdateCheck, *Error) {
	conclusions := make([]store.UpdateCheck, len(r.skills))
	var failure *Error
	for i, checked := range r.checked {
		switch {
		case checked.err == nil && checked.changed:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateAvailable), Revision: r.revision, Content: skillfile.Sum(checked.latest.Files), Found: true}
		case checked.err == nil:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateCurrent), Revision: r.revision, Found: true}
		case missingSkill(checked.err):
			conclusions[i] = store.UpdateCheck{Status: string(UpdateDeleted), Revision: r.revision, Renamed: r.renames[i].latest.Name, Content: r.renames[i].content}
		default:
			conclusions[i] = store.UpdateCheck{Status: string(UpdateFailed)}
			if failure == nil {
				failure = ErrorOf(checked.err)
			}
		}
	}
	return conclusions, failure
}

func (l *Library) collectionCurrent(ctx context.Context, id CollectionID) (*collectionRead, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	rows, err := l.store.ListCollectionSkills(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	read := &collectionRead{}
	for _, row := range rows {
		skill, state, err := l.currentOf(ctx, row)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		state.origin = source.Have{Name: skill.Name, Files: state.files, State: state.snapshot}
		if state.followsAuthor() {
			origin, err := l.store.Origin(ctx, state.versionID)
			if err != nil {
				return nil, err
			}
			state.origin.Files, state.origin.State = origin.Files, source.State(origin.State)
		}
		rejected, err := l.store.RejectedRenames(ctx, int64(skill.ID))
		if err != nil {
			return nil, err
		}
		link, err := l.store.RolledBackRename(ctx, int64(skill.ID))
		if err != nil {
			return nil, err
		}
		read.linked = append(read.linked, link)
		read.skills = append(read.skills, skill)
		read.states = append(read.states, state)
		read.rejected = append(read.rejected, rejected)
	}
	if len(read.skills) == 0 {
		return nil, nil
	}
	if read.replica, err = l.store.Replica(ctx); err != nil {
		return nil, err
	}
	if read.listedAt, err = l.store.Listed(ctx, read.states[0].source.ID); err != nil {
		return nil, err
	}
	known, err := l.store.SourceSkills(ctx, read.states[0].source.Type, read.states[0].source.Locator)
	if err != nil {
		return nil, err
	}
	read.seen = known.Seen
	return read, nil
}

func (l *Library) saveCollectionRead(ctx context.Context, read *collectionRead, conclusions []store.UpdateCheck) ([]Skill, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	saved := []Skill{}
	err := l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.RequireReplica(ctx, read.replica); err != nil {
			return err
		}
		now := clockNow()
		if read.openErr == nil {
			sourceID := read.states[0].source.ID
			listedAt, err := tx.Listed(ctx, sourceID)
			if err != nil {
				return err
			}
			switch {
			case listedAt.Serial != read.listedAt.Serial:
			case read.listed != nil:
				err = tx.ListSourceSkills(ctx, sourceID, read.listed, read.revision, false)
			case read.revision == read.listedAt.Revision:
				err = tx.ConfirmListed(ctx, sourceID)
			}
			if err != nil {
				return err
			}
		}
		for i, skill := range read.skills {
			id := int64(skill.ID)
			current, err := tx.GetSkill(ctx, id)
			if errors.Is(err, store.ErrNotFound) || err == nil && (current.Source.ID != read.states[i].source.ID || current.Name != skill.Name) {
				continue
			}
			if err != nil {
				return err
			}
			versionID := current.VersionID
			stored, err := tx.UpdateCheck(ctx, id)
			if err != nil {
				return err
			}
			conclusion := conclusions[i]
			conclusion.Since = read.states[i].checkSerial
			written := stored.Serial != read.states[i].checkSerial
			if versionID == read.states[i].versionID && (!written || UpdateCheckStatus(stored.Status) == UpdateFailed) {
				conclusion.At = now
				if err := tx.SaveUpdateCheck(ctx, id, conclusion); err != nil {
					return err
				}
				if conclusion.Found && read.linked[i].ID != 0 {
					if err := tx.ForgetRename(ctx, read.linked[i].ID); err != nil {
						return err
					}
				}
			}
			row, err := tx.GetSkill(ctx, id)
			if err != nil {
				return err
			}
			updated, err := l.skillOf(row)
			if err != nil {
				return err
			}
			saved = append(saved, updated)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func missingSkill(err error) bool {
	missing, ok := errors.AsType[*source.NotFoundError](err)
	return ok && missing.What == source.MissingSkill
}

type checkedSkill struct {
	changed bool
	latest  source.Skill
	err     error
}

func haves(states []currentVersion) []source.Have {
	result := make([]source.Have, len(states))
	for i, state := range states {
		result[i] = state.origin
	}
	return result
}

func (c currentVersion) followsAuthor() bool {
	return c.base != 0 && c.source.Type != local.Type
}

func keepEdits(checked []checkedSkill, states []currentVersion) {
	for i := range checked {
		if !states[i].followsAuthor() || checked[i].err != nil {
			continue
		}
		if checked[i].changed && !skillfile.Equal(checked[i].latest.Files, states[i].files) {
			continue
		}
		checked[i] = checkedSkill{latest: source.Skill{Name: states[i].origin.Name, Files: states[i].files}}
	}
}

func checkSkills(ctx context.Context, snapshot source.Snapshot, haves []source.Have, needFiles bool) []checkedSkill {
	for {
		revision := snapshot.Revision()
		results := make([]checkedSkill, len(haves))
		for i, have := range haves {
			if err := ctx.Err(); err != nil {
				results[i].err = err
				continue
			}
			results[i].changed, results[i].latest, results[i].err = checkSnapshot(ctx, snapshot, have, needFiles)
		}
		if snapshot.Revision() == revision {
			return results
		}
	}
}

func checkSnapshot(ctx context.Context, snapshot source.Snapshot, have source.Have, needFiles bool) (bool, source.Skill, error) {
	status, state, err := snapshot.Check(ctx, have)
	if err != nil {
		return false, source.Skill{}, unreadableSkill(ctx, snapshot, have.Name, err)
	}
	switch {
	case status == source.Unchanged:
		return false, source.Skill{Name: have.Name, Files: have.Files, State: state}, nil
	case status == source.Changed && !needFiles:
		return true, source.Skill{}, nil
	}
	fetched, err := snapshot.Fetch(ctx, []string{have.Name})
	if err != nil {
		return false, source.Skill{}, unreadableSkill(ctx, snapshot, have.Name, err)
	}
	latest := fetched[0]
	return status == source.Changed || !skillfile.Equal(have.Files, latest.Files), latest, nil
}

func unreadableSkill(ctx context.Context, snapshot source.Snapshot, name string, err error) error {
	if !missingSkill(err) {
		return err
	}
	_, diagnostics, candidatesErr := snapshot.Candidates(ctx)
	if none, ok := errors.AsType[*discovery.NoCandidatesError](candidatesErr); ok {
		diagnostics = none.Diagnostics
	} else if candidatesErr != nil {
		return err
	}
	for _, diagnostic := range diagnostics {
		if path.Base(diagnostic.Path) == name {
			return &source.IncompleteError{Skill: name, Err: fmt.Errorf("%s: %s: %w", diagnostic.Path, diagnostic.Reason, source.ErrInvalid)}
		}
	}
	return err
}

const renameSimilarity = 0.6

func guessRenames(ctx context.Context, read *collectionRead) map[int]renameGuess {
	var missing []int
	for i, checked := range read.checked {
		if missingSkill(checked.err) {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	names := read.listed
	if names == nil {
		var err error
		if names, err = read.snapshot.Names(ctx); err != nil {
			return nil
		}
	}
	inLibrary := func(name string) bool {
		return slices.ContainsFunc(read.skills, func(skill Skill) bool { return skill.Name == name })
	}
	var candidates []string
	for _, name := range names {
		linked := slices.ContainsFunc(missing, func(i int) bool { return read.linked[i].Name == name })
		if (linked || !read.seen[name]) && !inLibrary(name) && !slices.Contains(candidates, name) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	fetched, err := read.snapshot.Fetch(ctx, candidates)
	if err != nil {
		return nil
	}
	type pair struct {
		skill     int
		candidate int
		score     float64
	}
	var pairs []pair
	contents := make([]string, len(fetched))
	for j, candidate := range fetched {
		contents[j] = skillfile.Sum(candidate.Files)
	}
	for _, i := range missing {
		for j, candidate := range fetched {
			if slices.Contains(read.rejected[i][candidate.Name], contents[j]) {
				continue
			}
			if read.linked[i].Name == candidate.Name {
				pairs = append(pairs, pair{i, j, 2})
			} else if score := skillfile.Similarity(read.states[i].files, candidate.Files); score >= renameSimilarity && !read.seen[candidate.Name] {
				pairs = append(pairs, pair{i, j, score})
			}
		}
	}
	slices.SortStableFunc(pairs, func(a, b pair) int { return cmp.Compare(b.score, a.score) })
	guesses := map[int]renameGuess{}
	used := map[int]bool{}
	for _, p := range pairs {
		if _, done := guesses[p.skill]; done || used[p.candidate] {
			continue
		}
		used[p.candidate] = true
		guesses[p.skill] = renameGuess{latest: fetched[p.candidate], content: contents[p.candidate]}
	}
	return guesses
}
