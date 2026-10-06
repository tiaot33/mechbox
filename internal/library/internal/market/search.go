package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	searchTimeout = 30 * time.Second
	searchLimit   = 20
)

type Skill struct {
	ID        string
	Name      string
	Source    string
	Installs  *int64
	DetailURL string
}

func Search(ctx context.Context, query string) ([]Skill, error) {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := get(ctx, baseURL+"/api/search?"+url.Values{"q": {query}, "limit": {fmt.Sprint(searchLimit)}}.Encode())
	if err != nil {
		return nil, fmt.Errorf("search skills: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		sentinel := ErrUnreachable
		if response.StatusCode == 401 || response.StatusCode == 403 {
			sentinel = ErrPermission
		}
		return nil, fmt.Errorf("search skills: HTTP %d: %w", response.StatusCode, sentinel)
	}
	body, err := readBody(ctx, response)
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
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
		return nil, fmt.Errorf("read search response: invalid response format: %w", ErrInvalidResponse)
	}
	skills := []Skill{}
	for i, skill := range payload.Skills {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i == searchLimit {
			break
		}
		if strings.TrimSpace(skill.ID) == "" || strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Source) == "" || (skill.Installs != nil && *skill.Installs < 0) {
			return nil, fmt.Errorf("read search response: missing or invalid result fields: %w", ErrInvalidResponse)
		}
		segments := strings.Split(skill.ID, "/")
		for i, segment := range segments {
			segments[i] = url.PathEscape(segment)
		}
		skills = append(skills, Skill{ID: skill.ID, Name: skill.Name, Source: skill.Source, Installs: skill.Installs, DetailURL: baseURL + "/" + strings.Join(segments, "/")})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return skills, nil
}
