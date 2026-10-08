package library

import (
	"cmp"
	"context"
	"slices"
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
}

// ListCollections 按首次从该来源入库的先后列出全部集合;只有一个技能的集合照样列出,
// 最后一个技能移除后集合不再出现。库里没有技能时返回空列表。
func (l *Library) ListCollections(ctx context.Context) (collections []Collection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	skills, err := l.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(skills, func(a, b Skill) int {
		return cmp.Or(cmp.Compare(a.CollectionID, b.CollectionID), cmp.Compare(a.ID, b.ID))
	})
	collections = []Collection{}
	for _, skill := range skills {
		if len(collections) == 0 || collections[len(collections)-1].ID != skill.CollectionID {
			collections = append(collections, Collection{ID: skill.CollectionID, Source: skill.Source})
		}
		last := &collections[len(collections)-1]
		last.Skills = append(last.Skills, skill.ID)
	}
	return collections, nil
}
