package library

import (
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/agentsettings"
	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
)

// AgentSettings 是技能对 Claude Code、Codex 声明的设置:只有那个 Agent 才读的字段,决定它怎样用这个技能。
// Claude Code 的取自 SKILL.md 开头的 frontmatter,Codex 的取自技能文件夹里的 agents/openai.yaml。
// 只给出和默认不同的;写错了、解析不出来的按 Agent 自己的做法当作没写,不报错。
// 库只读出来给界面看,不据此改变安装、同名判断或其他任何行为;Agent 实际怎样做还取决于它自己的设置和版本。
// 其他 Agent 读哪些字段不知道,这里不给。
type AgentSettings struct {
	ClaudeCode *ClaudeCodeSettings `json:"claude_code,omitempty"` // Claude Code 的;全是默认值时为 nil
	Codex      *CodexSettings      `json:"codex,omitempty"`       // Codex 的;全是默认值时为 nil
}

// ClaudeCodeSettings 是 SKILL.md frontmatter 里 Claude Code 才读的字段,零值就是默认。
type ClaudeCodeSettings struct {
	// ModelInvocationDisabled 对应 disable-model-invocation: true:Claude 不会自动调用它,只能用 /名称 手动调用。
	ModelInvocationDisabled bool `json:"model_invocation_disabled,omitzero"`
	// UserInvocationDisabled 对应 user-invocable: false:不在 / 菜单里,用 /名称 也调不出来,只留给 Claude 自动调用。
	UserInvocationDisabled bool `json:"user_invocation_disabled,omitzero"`
	// AllowedTools 是 allowed-tools:调用这个技能的那一轮里,这些工具不用用户确认就能用,如 Bash(git add *)。
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DisallowedTools 是 disallowed-tools:这个技能在用时,这些工具从 Claude 能用的工具里拿掉。
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
	// Hooks 是 hooks:调用这个技能时注册、之后整个会话都会触发的 hooks,按事件名排。
	Hooks []ClaudeCodeHook `json:"hooks,omitempty"`
	// Paths 是 paths:只在处理匹配这些 glob 的文件时才自动调用。
	Paths []string `json:"paths,omitempty"`
	// WhenToUse 是 when_to_use:接在描述后面、帮 Claude 判断什么时候调用的说明。
	WhenToUse string `json:"when_to_use,omitempty"`
	// ArgumentHint 是 argument-hint:手动调用时补全里提示要带的参数。
	ArgumentHint string `json:"argument_hint,omitempty"`
	// Model 是 model:调用时这一轮换用的模型;inherit(沿用当前模型)当作没写。
	Model string `json:"model,omitempty"`
	// Effort 是 effort:调用时这一轮的思考深度,如 high。
	Effort string `json:"effort,omitempty"`
	// Fork 对应 context: fork:在单独的 subagent 里运行;Agent 是那个 subagent 的类型(agent),没写时是 general-purpose,这里为空。
	Fork  bool   `json:"fork,omitzero"`
	Agent string `json:"agent,omitempty"`
	// Shell 是 shell:技能里内联命令用的 shell;bash(默认)当作没写,如 powershell。
	Shell string `json:"shell,omitempty"`
}

// ClaudeCodeHook 是技能注册的一个 hook。
type ClaudeCodeHook struct {
	Event   string `json:"event"`             // 触发的事件,如 PreToolUse
	Matcher string `json:"matcher,omitempty"` // 只在匹配的工具等上触发,如 Bash;空表示都触发
	Type    string `json:"type"`              // command、http、mcp_tool、prompt、agent
	// Detail 是它做什么:command 是要执行的命令(连同参数),http 是请求的地址,mcp_tool 是「服务/工具」,prompt 与 agent 是提示词。
	Detail string `json:"detail,omitempty"`
}

// CodexSettings 是 agents/openai.yaml 里 Codex 读的设置,零值就是默认。只管 ChatGPT 界面显示的 interface 不给。
type CodexSettings struct {
	// ImplicitInvocationDisabled 对应 policy.allow_implicit_invocation: false:Codex 不会自动调用它,只能用 $名称 手动调用。
	ImplicitInvocationDisabled bool `json:"implicit_invocation_disabled,omitzero"`
	// Products 是 policy.products:只在这几个产品里用(chatgpt、codex、atlas,统一成小写);空表示不限。
	Products []string `json:"products,omitempty"`
	// Tools 是 dependencies.tools:技能要接入的工具,如 MCP 服务。按 Codex 的规则只留下 type 和 value 都有的。
	Tools []CodexTool `json:"tools,omitempty"`
}

// CodexTool 是技能要接入的一个工具。
type CodexTool struct {
	Type        string `json:"type"`  // 种类,如 mcp
	Value       string `json:"value"` // 名字,如 MCP 服务名
	Description string `json:"description,omitempty"`
	Transport   string `json:"transport,omitempty"` // 连接方式,如 streamable_http
	Command     string `json:"command,omitempty"`   // 启动它要执行的命令
	URL         string `json:"url,omitempty"`       // 连接的地址
}

type parsedSettings struct {
	claude    agentsettings.Claude
	codex     agentsettings.Codex
	hasClaude bool
	hasCodex  bool
	skillMD   string
}

func parseSettings(skillMD, codex []byte) parsedSettings {
	parsed := parsedSettings{skillMD: "SKILL.md"}
	parsed.claude, parsed.hasClaude = agentsettings.ParseClaude(skillMD)
	parsed.codex, parsed.hasCodex = agentsettings.ParseCodex(codex)
	return parsed
}

func parseFileSettings(files []skillfile.File) parsedSettings {
	var manifest *skillfile.File
	var codex []byte
	for i, file := range files {
		switch {
		case file.Path == "SKILL.md" || manifest == nil && strings.EqualFold(file.Path, "SKILL.md"):
			manifest = &files[i]
		case file.Path == agentsettings.CodexFile:
			codex = file.Content
		}
	}
	if manifest == nil {
		return parseSettings(nil, codex)
	}
	parsed := parseSettings(manifest.Content, codex)
	parsed.skillMD = manifest.Path
	return parsed
}

func agentSettingsOf(skillMD, codex []byte) *AgentSettings {
	return parseSettings(skillMD, codex).public()
}

func (p parsedSettings) public() *AgentSettings {
	if !p.hasClaude && !p.hasCodex {
		return nil
	}
	var result AgentSettings
	if p.hasClaude {
		c := p.claude
		result.ClaudeCode = &ClaudeCodeSettings{
			ModelInvocationDisabled: c.ModelInvocationDisabled,
			UserInvocationDisabled:  c.UserInvocationDisabled,
			AllowedTools:            c.AllowedTools,
			DisallowedTools:         c.DisallowedTools,
			Hooks:                   publicHooks(c.Hooks),
			Paths:                   c.Paths,
			WhenToUse:               c.WhenToUse,
			ArgumentHint:            c.ArgumentHint,
			Model:                   c.Model,
			Effort:                  c.Effort,
			Fork:                    c.Fork,
			Agent:                   c.Agent,
			Shell:                   c.Shell,
		}
	}
	if p.hasCodex {
		c := p.codex
		result.Codex = &CodexSettings{ImplicitInvocationDisabled: c.ImplicitInvocationDisabled, Products: c.Products, Tools: publicTools(c.Tools)}
	}
	return &result
}

func publicHooks(hooks []agentsettings.Hook) []ClaudeCodeHook {
	var result []ClaudeCodeHook
	for _, hook := range hooks {
		result = append(result, ClaudeCodeHook(hook))
	}
	return result
}

func publicTools(tools []agentsettings.Tool) []CodexTool {
	var result []CodexTool
	for _, tool := range tools {
		result = append(result, CodexTool(tool))
	}
	return result
}

func settingsRisks(from, to []skillfile.File) []UpdateRisk {
	old, latest := parseFileSettings(from), parseFileSettings(to)
	var risks []UpdateRisk
	if added := addedItems(latest.claude.AllowedTools, old.claude.AllowedTools, func(a, b string) bool { return a == b }); len(added) > 0 {
		risks = append(risks, UpdateRisk{Kind: RiskAllowedToolsAdded, Path: latest.skillMD, Agent: AgentClaudeCode, Items: added})
	}
	if added := addedItems(latest.claude.Hooks, old.claude.Hooks, func(a, b agentsettings.Hook) bool { return a == b }); len(added) > 0 {
		risks = append(risks, UpdateRisk{Kind: RiskHooksChanged, Path: latest.skillMD, Agent: AgentClaudeCode, Hooks: publicHooks(added)})
	}
	sameTool := func(a, b agentsettings.Tool) bool {
		return a.Type == b.Type && a.Value == b.Value && a.Transport == b.Transport && a.Command == b.Command && a.URL == b.URL
	}
	if added := addedItems(latest.codex.Tools, old.codex.Tools, sameTool); len(added) > 0 {
		risks = append(risks, UpdateRisk{Kind: RiskToolsAdded, Path: agentsettings.CodexFile, Agent: AgentCodex, Tools: publicTools(added)})
	}
	if old.claude.ModelInvocationDisabled && !latest.claude.ModelInvocationDisabled {
		risks = append(risks, UpdateRisk{Kind: RiskAutoInvocationEnabled, Path: latest.skillMD, Agent: AgentClaudeCode})
	}
	if old.codex.ImplicitInvocationDisabled && !latest.codex.ImplicitInvocationDisabled {
		risks = append(risks, UpdateRisk{Kind: RiskAutoInvocationEnabled, Path: agentsettings.CodexFile, Agent: AgentCodex})
	}
	return risks
}

func addedItems[T any](items, existing []T, same func(a, b T) bool) []T {
	var added []T
	for _, item := range items {
		found := func(x T) bool { return same(x, item) }
		if !slices.ContainsFunc(existing, found) && !slices.ContainsFunc(added, found) {
			added = append(added, item)
		}
	}
	return added
}
