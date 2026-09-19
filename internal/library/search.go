package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (l *sqliteLibrary) SearchSkills(ctx context.Context, query string) (result SearchResult, err error) {
	defer func() { err = typedError(err) }()
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return result, &Error{Code: InvalidArgument, Message: "search query must not be empty"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://skills.sh/api/search?"+url.Values{"q": {query}, "limit": {"20"}}.Encode(), nil)
	if err != nil {
		return result, err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fetchError(SourceUnreachable, "search skills", errors.New("search service is unavailable"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := SourceUnreachable
		if response.StatusCode == 401 || response.StatusCode == 403 {
			code = Permission
		}
		return result, fetchError(code, "search skills", fmt.Errorf("HTTP %d", response.StatusCode))
	}
	const maxSearchSize = 1 << 20
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, maxSearchSize+1))
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, fetchError(SourceUnreachable, "read search response", err)
	}
	if len(body) > maxSearchSize {
		return result, fetchError(SourceInvalid, "read search response", errors.New("response exceeds size limit"))
	}
	var payload struct {
		Skills []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Source   string `json:"source"`
			Installs *int64 `json:"installs"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Skills == nil {
		return result, fetchError(SourceInvalid, "read search response", errors.New("invalid response format"))
	}
	result.Skills = []SearchSkill{}
	for i, skill := range payload.Skills {
		if ctx.Err() != nil {
			return SearchResult{}, ctx.Err()
		}
		if i == 20 {
			break
		}
		if strings.TrimSpace(skill.ID) == "" || strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Source) == "" || (skill.Installs != nil && *skill.Installs < 0) {
			return SearchResult{}, fetchError(SourceInvalid, "read search response", errors.New("missing or invalid result fields"))
		}
		source, err := parseGitHubInput(skill.Source, ResolveSourceOptions{})
		if err != nil || source.tree != "" || source.ref != "" || source.name != "" || !validSelectionName(skill.Name) || strings.Contains(skill.Name, "#") {
			result.Diagnostics = append(result.Diagnostics, SearchDiagnostic{ID: skill.ID, Reason: "search result does not identify a supported GitHub repository and Skill name"})
			continue
		}
		repository := strings.TrimSuffix(strings.TrimPrefix(source.repository, "https://github.com/"), ".git")
		// id 仅用于展示链接,按路径段转义;下载始终使用解析后的 GitHub 来源。
		segments := strings.Split(skill.ID, "/")
		for i, segment := range segments {
			segments[i] = url.PathEscape(segment)
		}
		result.Skills = append(result.Skills, SearchSkill{ID: skill.ID, Name: skill.Name, SourceInput: repository + "@" + skill.Name, DetailURL: "https://skills.sh/" + strings.Join(segments, "/"), Installs: skill.Installs})
	}
	return result, ctx.Err()
}
