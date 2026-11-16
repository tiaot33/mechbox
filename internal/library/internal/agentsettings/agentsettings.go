package agentsettings

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const CodexFile = "agents/openai.yaml"

type Claude struct {
	ModelInvocationDisabled bool
	UserInvocationDisabled  bool
	AllowedTools            []string
	DisallowedTools         []string
	Hooks                   []Hook
	Paths                   []string
	WhenToUse               string
	ArgumentHint            string
	Model                   string
	Effort                  string
	Fork                    bool
	Agent                   string
	Shell                   string
}

type Hook struct {
	Event   string
	Matcher string
	Type    string
	Detail  string
}

type Codex struct {
	ImplicitInvocationDisabled bool
	Products                   []string
	Tools                      []Tool
}

type Tool struct {
	Type        string
	Value       string
	Description string
	Transport   string
	Command     string
	URL         string
}

func ParseClaude(skillMD []byte) (Claude, bool) {
	front, ok := frontmatter(skillMD)
	if !ok {
		return Claude{}, false
	}
	var fields map[string]any
	if err := yaml.Unmarshal(front, &fields); err != nil || fields == nil {
		return Claude{}, false
	}
	var result Claude
	result.ModelInvocationDisabled = boolean(fields["disable-model-invocation"]) == "true"
	result.UserInvocationDisabled = boolean(fields["user-invocable"]) == "false"
	result.AllowedTools = tools(fields["allowed-tools"])
	result.DisallowedTools = tools(fields["disallowed-tools"])
	result.Hooks = hooks(fields["hooks"])
	result.Paths = list(fields["paths"], func(r rune) bool { return r == ',' })
	result.WhenToUse = text(fields["when_to_use"])
	result.ArgumentHint = text(fields["argument-hint"])
	if result.Model = text(fields["model"]); result.Model == "inherit" {
		result.Model = ""
	}
	result.Effort = text(fields["effort"])
	if result.Fork = text(fields["context"]) == "fork"; result.Fork {
		result.Agent = text(fields["agent"])
	}
	if result.Shell = strings.ToLower(text(fields["shell"])); result.Shell == "bash" {
		result.Shell = ""
	}
	return result, !isZeroClaude(result)
}

func isZeroClaude(c Claude) bool {
	return !c.ModelInvocationDisabled && !c.UserInvocationDisabled && len(c.AllowedTools) == 0 && len(c.DisallowedTools) == 0 &&
		len(c.Hooks) == 0 && len(c.Paths) == 0 && c.WhenToUse == "" && c.ArgumentHint == "" && c.Model == "" && c.Effort == "" &&
		!c.Fork && c.Shell == ""
}

func frontmatter(content []byte) ([]byte, bool) {
	first, rest, ok := strings.Cut(string(content), "\n")
	if !ok || strings.TrimSuffix(first, "\r") != "---" {
		return nil, false
	}
	var front strings.Builder
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.TrimSuffix(line, "\r") == "---" {
			return []byte(front.String()), true
		}
		front.WriteString(line)
		front.WriteByte('\n')
	}
	return nil, false
}

func boolean(value any) string {
	switch v := value.(type) {
	case bool:
		return fmt.Sprint(v)
	case int:
		if v == 1 {
			return "true"
		}
		if v == 0 {
			return "false"
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on", "1":
			return "true"
		case "false", "no", "off", "0":
			return "false"
		}
	}
	return ""
}

func text(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case int, float64:
		return fmt.Sprint(v)
	}
	return ""
}

func tools(value any) []string {
	switch v := value.(type) {
	case string:
		return splitTools(v)
	case []any:
		var result []string
		for _, item := range v {
			if s := text(item); s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func splitTools(value string) []string {
	var result []string
	var current strings.Builder
	depth := 0
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			result = append(result, s)
		}
		current.Reset()
	}
	for _, r := range value {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0 && (r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
			continue
		}
		current.WriteRune(r)
	}
	flush()
	return result
}

func list(value any, separator func(rune) bool) []string {
	switch v := value.(type) {
	case string:
		var result []string
		for _, item := range strings.FieldsFunc(v, separator) {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
		return result
	case []any:
		var result []string
		for _, item := range v {
			if s := text(item); s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func hooks(value any) []Hook {
	events, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	var result []Hook
	for _, event := range sortedKeys(events) {
		groups, _ := events[event].([]any)
		for _, group := range groups {
			fields, ok := group.(map[string]any)
			if !ok {
				continue
			}
			matcher := text(fields["matcher"])
			handlers, _ := fields["hooks"].([]any)
			for _, handler := range handlers {
				h, ok := handler.(map[string]any)
				if !ok {
					continue
				}
				kind := text(h["type"])
				if kind == "" {
					continue
				}
				result = append(result, Hook{Event: event, Matcher: matcher, Type: kind, Detail: hookDetail(kind, h)})
			}
		}
	}
	return result
}

func hookDetail(kind string, h map[string]any) string {
	switch kind {
	case "command":
		parts := []string{text(h["command"])}
		if args, ok := h["args"].([]any); ok {
			for _, arg := range args {
				parts = append(parts, text(arg))
			}
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	case "http":
		return text(h["url"])
	case "mcp_tool":
		server, tool := text(h["server"]), text(h["tool"])
		if server == "" || tool == "" {
			return server + tool
		}
		return server + "/" + tool
	default:
		return text(h["prompt"])
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type codexFile struct {
	Dependencies *struct {
		Tools []struct {
			Type        *string `yaml:"type"`
			Value       *string `yaml:"value"`
			Description *string `yaml:"description"`
			Transport   *string `yaml:"transport"`
			Command     *string `yaml:"command"`
			URL         *string `yaml:"url"`
		} `yaml:"tools"`
	} `yaml:"dependencies"`
	Policy *struct {
		AllowImplicitInvocation *bool    `yaml:"allow_implicit_invocation"`
		Products                []string `yaml:"products"`
	} `yaml:"policy"`
}

var products = map[string]string{"chatgpt": "chatgpt", "CHATGPT": "chatgpt", "codex": "codex", "CODEX": "codex", "atlas": "atlas", "ATLAS": "atlas"}

func ParseCodex(content []byte) (Codex, bool) {
	if len(content) == 0 {
		return Codex{}, false
	}
	var file codexFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return Codex{}, false
	}
	var result Codex
	if file.Policy != nil {
		result.ImplicitInvocationDisabled = file.Policy.AllowImplicitInvocation != nil && !*file.Policy.AllowImplicitInvocation
		for _, product := range file.Policy.Products {
			normalized, ok := products[product]
			if !ok {
				return Codex{}, false
			}
			if !slices.Contains(result.Products, normalized) {
				result.Products = append(result.Products, normalized)
			}
		}
	}
	if file.Dependencies != nil {
		for _, tool := range file.Dependencies.Tools {
			kind, okType := limited(tool.Type, 64)
			value, okValue := limited(tool.Value, 1024)
			if !okType || !okValue {
				continue
			}
			description, _ := limited(tool.Description, 1024)
			transport, _ := limited(tool.Transport, 64)
			command, _ := limited(tool.Command, 1024)
			url, _ := limited(tool.URL, 1024)
			result.Tools = append(result.Tools, Tool{Type: kind, Value: value, Description: description, Transport: transport, Command: command, URL: url})
		}
	}
	return result, result.ImplicitInvocationDisabled || len(result.Products) > 0 || len(result.Tools) > 0
}

func limited(value *string, max int) (string, bool) {
	if value == nil {
		return "", false
	}
	collapsed := strings.Join(strings.Fields(*value), " ")
	if collapsed == "" || utf8.RuneCountInString(collapsed) > max {
		return "", false
	}
	return collapsed, true
}
