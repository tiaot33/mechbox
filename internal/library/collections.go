package library

import (
	"cmp"
	"context"
	"slices"
)

// Collection 是按 Source 自动聚合的 skill 集合;派生数据,数据库里没有对应表。
// Skills 是精简摘要,不含文件正文。
type Collection struct {
	Source Source         `json:"source"`
	Skills []SkillSummary `json:"skills"`
}

// ListCollections 按 Source 聚合列出全部 skill。
func (l *Library) ListCollections(ctx context.Context) (collections []Collection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	skills, err := l.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(skills, func(a, b SkillSummary) int {
		return cmp.Or(cmp.Compare(a.Source.ID, b.Source.ID), cmp.Compare(a.ID, b.ID))
	})
	for _, skill := range skills {
		if len(collections) == 0 || collections[len(collections)-1].Source.ID != skill.Source.ID {
			collections = append(collections, Collection{Source: skill.Source})
		}
		last := len(collections) - 1
		collections[last].Skills = append(collections[last].Skills, skill)
	}
	return collections, nil
}
