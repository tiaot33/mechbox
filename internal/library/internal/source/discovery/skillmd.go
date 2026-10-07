package discovery

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

func ParseSkillMD(content []byte, dir string) (Candidate, error) {
	result := Candidate{Path: dir}
	first, rest, ok := strings.Cut(string(content), "\n")
	if !ok || strings.TrimSuffix(first, "\r") != "---" {
		return result, errors.New("missing YAML frontmatter")
	}
	var front strings.Builder
	closed := false
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.TrimSuffix(line, "\r") == "---" {
			closed = true
			break
		}
		front.WriteString(line)
		front.WriteByte('\n')
	}
	if !closed {
		return result, errors.New("unclosed YAML frontmatter")
	}
	var metadata map[string]any
	if err := yaml.Unmarshal([]byte(front.String()), &metadata); err != nil {
		return result, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	name, ok := metadata["name"].(string)
	if !ok || !ValidName(name) {
		return result, errors.New("name must be a non-empty safe string")
	}
	result.Name = name
	result.Description, ok = metadata["description"].(string)
	if !ok {
		return result, errors.New("description must be a string")
	}
	return result, nil
}

func ValidName(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value) && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00\r\n")
}
