package library

import (
	"context"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/market"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
)

// SearchSkills 只读搜索 skills.sh,最多返回 20 项;服务失败与空结果分别返回。
// 无法转换为可做入库预览的 GitHub 地址的条目直接过滤掉。
func (l *Library) SearchSkills(ctx context.Context, query string) (result []SearchSkill, err error) {
	defer func() { err = typedError(err) }()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, &Error{Code: InvalidArgument, Message: "search query must not be empty"}
	}
	skills, err := market.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	result = []SearchSkill{}
	for _, skill := range skills {
		repository, ok := github.PlainRepository(skill.Source)
		if !ok || !discovery.ValidName(skill.Name) || strings.Contains(skill.Name, "#") {
			continue
		}
		preview := PreviewFetchRequest{GitHub: &GitHubInput{Input: repository + "@" + skill.Name}}
		result = append(result, SearchSkill{ID: skill.ID, Name: skill.Name, DetailURL: skill.DetailURL, Installs: skill.Installs, Preview: preview})
	}
	return result, nil
}
