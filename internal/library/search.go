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

func (l *sqliteLibrary) SearchSkills(ctx context.Context, query string) (result []SearchSkill, err error) {
	defer func() { err = typedError(err) }()
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, &Error{Code: InvalidArgument, Message: "search query must not be empty"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://skills.sh/api/search?"+url.Values{"q": {query}, "limit": {"20"}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fetchError(SourceUnreachable, "search skills", errors.New("search service is unavailable"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := SourceUnreachable
		if response.StatusCode == 401 || response.StatusCode == 403 {
			code = Permission
		}
		return nil, fetchError(code, "search skills", fmt.Errorf("HTTP %d", response.StatusCode))
	}
	const maxSearchSize = 1 << 20
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, maxSearchSize+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fetchError(SourceUnreachable, "read search response", err)
	}
	if len(body) > maxSearchSize {
		return nil, fetchError(SourceInvalid, "read search response", errors.New("response exceeds size limit"))
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
		return nil, fetchError(SourceInvalid, "read search response", errors.New("invalid response format"))
	}
	result = []SearchSkill{}
	for i, skill := range payload.Skills {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if i == 20 {
			break
		}
		if strings.TrimSpace(skill.ID) == "" || strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Source) == "" || (skill.Installs != nil && *skill.Installs < 0) {
			return nil, fetchError(SourceInvalid, "read search response", errors.New("missing or invalid result fields"))
		}
		source, err := parseGitHubInput(GitHubInput{Input: skill.Source})
		if err != nil || source.tree != "" || source.ref != "" || source.name != "" || !validSelectionName(skill.Name) || strings.Contains(skill.Name, "#") {
			continue
		}
		repository := strings.TrimSuffix(strings.TrimPrefix(source.repository, "https://github.com/"), ".git")
		segments := strings.Split(skill.ID, "/")
		for i, segment := range segments {
			segments[i] = url.PathEscape(segment)
		}
		preview := PreviewFetchRequest{GitHub: &GitHubInput{Input: repository + "@" + skill.Name}}
		result = append(result, SearchSkill{ID: skill.ID, Name: skill.Name, DetailURL: "https://skills.sh/" + strings.Join(segments, "/"), Installs: skill.Installs, Preview: preview})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
