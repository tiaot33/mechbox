package library

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// CollectionID 是集合的身份;界面打开集合、从技能发起更新时都按它引用集合。
// 一个来源就是一个集合,同步拉回的库保留同样的 ID。
type CollectionID int64

// Collection 是从同一个来源入库的技能,按来源自动形成(见 ADR 0004);与手动创建的 Group 相对。
// 只给出技能 ID,技能信息从 ListSkills / GetSkill 取。
type Collection struct {
	ID     CollectionID `json:"id"`
	Source Source       `json:"source"` // 集合来自哪里
	Skills []SkillID    `json:"skills"` // 集合里的技能,按 ID 升序
	// NewSkills 是来源里的新技能名,按名字排序:用户上次从这个来源入库之后,检查更新才在来源里看到的技能。
	// 不计入角标,也不出现在更新里;入库走对这个集合的入库预览(PreviewFetchRequest.Collection),候选上 New 标出它们。
	// 用户点了「不感兴趣」(MarkNotInterested)的不再列出;来源里又没有了的也不再列出。
	NewSkills []string `json:"new_skills,omitempty"`
}

// ListCollections 按用户排的先后(ReorderCollections)列出全部集合,没排过的(从没排过、恢复了默认顺序,或排过之后才入库的)
// 按首次从该来源入库的先后接在后面;只有一个技能的集合照样列出,最后一个技能移除后集合不再出现,再入库时回到原来的位置。
// 没有来源的技能不在任何集合里。库里没有技能时返回空列表。
func (l *Library) ListCollections(ctx context.Context) (collections []Collection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	skills, err := l.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	newSkills, err := l.store.NewSkills(ctx)
	if err != nil {
		return nil, err
	}
	order, err := l.store.SourceOrder(ctx)
	if err != nil {
		return nil, err
	}
	rank := make(map[CollectionID]int, len(order))
	for i, id := range order {
		rank[CollectionID(id)] = i
	}
	slices.SortFunc(skills, func(a, b Skill) int {
		return cmp.Or(cmp.Compare(rank[a.CollectionID], rank[b.CollectionID]), cmp.Compare(a.CollectionID, b.CollectionID), cmp.Compare(a.ID, b.ID))
	})
	collections = []Collection{}
	for _, skill := range skills {
		if skill.CollectionID == 0 {
			continue
		}
		if len(collections) == 0 || collections[len(collections)-1].ID != skill.CollectionID {
			collections = append(collections, Collection{ID: skill.CollectionID, Source: skill.Source, NewSkills: newSkills[int64(skill.CollectionID)]})
		}
		last := &collections[len(collections)-1]
		last.Skills = append(last.Skills, skill.ID)
	}
	return collections, nil
}

// MarkNotInterested 记下用户对集合里这几个新技能点了「不感兴趣」:之后 Collection.NewSkills 不再列出它们,
// 入库预览里仍能找到,只是候选上不再标为新技能。名字不必是当前的新技能,重复调用无副作用。
// 集合 ID 不是正数、names 为空或含不合法的技能名返回 InvalidArgument(Details.Field 为 collection_id / names),集合不存在返回 NotFound。
func (l *Library) MarkNotInterested(ctx context.Context, id CollectionID, names []string) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return argumentError("mark not interested", "collection_id", "collection ID must be positive")
	}
	if len(names) == 0 {
		return argumentError("mark not interested", "names", "at least one skill name is required")
	}
	for _, name := range names {
		if !discovery.ValidName(name) {
			return argumentError("mark not interested", "names", "invalid skill name")
		}
	}
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.GetCollectionSource(ctx, int64(id)); errors.Is(err, store.ErrNotFound) {
			return &Error{Code: NotFound, Message: "mark not interested: collection not found", Err: err}
		} else if err != nil {
			return err
		}
		return tx.MarkSeen(ctx, int64(id), names)
	})
}

// ReorderCollections 按 ids 的先后排列集合,左栏把一个集合拖到另一个集合前后时调用:ids 里的排在前面,
// 没列出的按原来的先后接在后面;之后入库的新来源排在最后。ids 为空时恢复默认的先后,即首次入库的先后。
// 先后存在数据库里,随推送、拉回走。ids 有重复或不是正数返回 InvalidArgument(Details.Field 为 ids),
// 有集合不存在返回 NotFound。
func (l *Library) ReorderCollections(ctx context.Context, ids []CollectionID) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateOrder("reorder collections", ids); err != nil {
		return err
	}
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			if _, err := tx.GetCollectionSource(ctx, int64(id)); errors.Is(err, store.ErrNotFound) {
				return &Error{Code: NotFound, Message: "reorder collections: collection not found", Err: err}
			} else if err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return tx.SetSourcePositions(ctx, nil)
		}
		current, err := tx.SourceOrder(ctx)
		if err != nil {
			return err
		}
		return tx.SetSourcePositions(ctx, reordered(ids, current))
	})
}

func validateOrder[ID ~int64](operation string, ids []ID) error {
	seen := make(map[ID]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return argumentError(operation, "ids", "IDs must be positive")
		}
		if seen[id] {
			return argumentError(operation, "ids", "IDs must not repeat")
		}
		seen[id] = true
	}
	return nil
}

func reordered[ID ~int64](ids []ID, current []int64) []int64 {
	listed := make(map[int64]bool, len(ids))
	order := make([]int64, 0, len(current))
	for _, id := range ids {
		listed[int64(id)] = true
		order = append(order, int64(id))
	}
	for _, id := range current {
		if !listed[id] {
			order = append(order, id)
		}
	}
	return order
}
