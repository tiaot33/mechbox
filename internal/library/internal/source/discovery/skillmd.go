package discovery

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Problem string

const (
	NoFrontmatter       Problem = "no_frontmatter"
	UnclosedFrontmatter Problem = "unclosed_frontmatter"
	InvalidYAML         Problem = "invalid_yaml"
	UnquotedColon       Problem = "unquoted_colon"
	InvalidName         Problem = "invalid_name"
	NoDescription       Problem = "no_description"
)

type SkillMDError struct {
	Problem Problem
	Line    int
	Err     error
}

func (e *SkillMDError) Error() string { return e.Err.Error() }

func (e *SkillMDError) Unwrap() error { return e.Err }

var yamlLine = regexp.MustCompile(`line (\d+): `)

func ParseSkillMD(content []byte, dir string) (Candidate, error) {
	result := Candidate{Path: dir}
	first, rest, ok := strings.Cut(string(content), "\n")
	if !ok || strings.TrimSuffix(first, "\r") != "---" {
		return result, &SkillMDError{Problem: NoFrontmatter, Err: errors.New("missing YAML frontmatter")}
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
		return result, &SkillMDError{Problem: UnclosedFrontmatter, Err: errors.New("unclosed YAML frontmatter")}
	}
	var metadata map[string]any
	if err := yaml.Unmarshal([]byte(front.String()), &metadata); err != nil {
		problem := &SkillMDError{Problem: InvalidYAML, Err: fmt.Errorf("invalid YAML frontmatter: %w", err)}
		if match := yamlLine.FindStringSubmatch(err.Error()); match != nil {
			line, _ := strconv.Atoi(match[1])
			problem.Line = line + 1
		}
		if strings.Contains(err.Error(), "mapping values are not allowed") {
			problem.Problem = UnquotedColon
		}
		return result, problem
	}
	name, ok := metadata["name"].(string)
	if !ok || !ValidName(name) {
		return result, &SkillMDError{Problem: InvalidName, Err: errors.New("name must be a non-empty safe string")}
	}
	result.Name = name
	result.Description, ok = metadata["description"].(string)
	if !ok {
		return result, &SkillMDError{Problem: NoDescription, Err: errors.New("description must be a string")}
	}
	return result, nil
}

func ValidName(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value) && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00\r\n")
}
