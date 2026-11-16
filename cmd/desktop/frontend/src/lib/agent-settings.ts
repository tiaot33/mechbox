// 技能对 Claude Code、Codex 声明的设置(技能库从 SKILL.md 的 frontmatter 与 agents/openai.yaml 读出)在界面上的说法。
// 只说和默认不同的;这是技能自己声明的，Agent 实际怎样做还取决于它自己的设置和版本，措辞上不打包票。
// 作用域页的行只说调用方式，技能详情逐项说，入库预览只说权限(不用确认就能用的工具、hooks、要接入的工具)。
import { AgentCode, type AgentSettings, type ClaudeCodeSettings, type CodexSettings } from "@bindings/internal/library";
import { messages } from "./i18n";

export type Invocation = "manual" | "auto" | "neither";

// claudeInvocation 是 Claude Code 怎样调用它：只能手动、只能自动、都不行;默认(两样都行)为 undefined。
export function claudeInvocation(c: ClaudeCodeSettings | null | undefined): Invocation | undefined {
  if (!c) return undefined;
  if (c.model_invocation_disabled && c.user_invocation_disabled) return "neither";
  if (c.model_invocation_disabled) return "manual";
  if (c.user_invocation_disabled) return "auto";
  return undefined;
}

// codexInvocation 是 Codex 怎样调用它:policy.products 写了、又不含 codex 时 Codex 不加载它，调用不了;关掉了自动调用就只能手动。
export function codexInvocation(c: CodexSettings | null | undefined): Invocation | undefined {
  if (c?.products?.length && !c.products.includes("codex")) return "neither";
  return c?.implicit_invocation_disabled ? "manual" : undefined;
}

export type Note = { text: string; title: string };

// invocationNote 是作用域页一行上写的调用方式：只看 agents 里的内置 Agent(作用域的 Agent,项目里共用目录时是读这个目录的几个),
// 和默认一样、或这几个都不是 Claude Code、Codex 时为 undefined。几个 Agent 说法一样时只写一次，悬停分别说明。
export function invocationNote(settings: AgentSettings | null | undefined, agents: AgentCode[], name: string): Note | undefined {
  const t = messages().agentSettings;
  const notes: Note[] = [];
  if (agents.includes(AgentCode.AgentClaudeCode)) {
    const how = claudeInvocation(settings?.claude_code);
    if (how) notes.push({ text: t.invocationShort[how], title: t.claudeInvocationTitle(how, name) });
  }
  if (agents.includes(AgentCode.AgentCodex)) {
    const how = codexInvocation(settings?.codex);
    if (how) notes.push({ text: t.invocationShort[how], title: how === "neither" ? t.codexProductsTitle(productNames(settings?.codex?.products)) : t.codexInvocationTitle(name) });
  }
  if (!notes.length) return undefined;
  return { text: [...new Set(notes.map((n) => n.text))].join(" · "), title: [...notes.map((n) => n.title), t.declared].join("\n") };
}

export type SettingRow = { key: string; label: string; values: string[]; mono?: boolean };
export type SettingSection = { agent: AgentCode; rows: SettingRow[] };

// settingSections 是技能详情里每个 Agent 一节、逐项写出的设置;某个 Agent 全是默认时没有那一节。first 是从作用域页打开时那个作用域的 Agent,排在前面。
export function settingSections(settings: AgentSettings | null | undefined, name: string, first?: AgentCode): SettingSection[] {
  const t = messages().agentSettings;
  const sections: SettingSection[] = [];
  const c = settings?.claude_code;
  if (c) {
    const how = claudeInvocation(c);
    const run = [
      c.fork && t.fork(c.agent ?? ""),
      c.model && t.model(c.model),
      c.effort && t.effort(c.effort),
      c.shell && t.shell(c.shell),
    ].filter((x): x is string => !!x);
    const rows: (SettingRow | false)[] = [
      !!how && { key: "invocation", label: t.invocation, values: [t.claudeInvocation(how, name)] },
      !!c.paths?.length && { key: "paths", label: t.paths, values: c.paths, mono: true },
      !!c.when_to_use && { key: "when", label: t.whenToUse, values: [c.when_to_use] },
      !!c.allowed_tools?.length && { key: "allowed", label: t.allowedTools, values: c.allowed_tools, mono: true },
      !!c.hooks?.length && { key: "hooks", label: t.hooks, values: c.hooks.map(hookText), mono: true },
      !!c.disallowed_tools?.length && { key: "disallowed", label: t.disallowedTools, values: c.disallowed_tools, mono: true },
      run.length > 0 && { key: "run", label: t.run, values: run },
      !!c.argument_hint && { key: "argument", label: t.argumentHint, values: [c.argument_hint], mono: true },
    ];
    sections.push({ agent: AgentCode.AgentClaudeCode, rows: rows.filter((x): x is SettingRow => !!x) });
  }
  const x = settings?.codex;
  if (x) {
    const rows: (SettingRow | false)[] = [
      !!codexInvocation(x) && { key: "invocation", label: t.invocation, values: [codexInvocation(x) === "neither" ? t.codexNeither : t.codexInvocation(name)] },
      !!x.products?.length && { key: "products", label: t.products, values: [productNames(x.products)] },
      !!x.tools?.length && { key: "tools", label: t.tools, values: x.tools.map(toolText), mono: true },
    ];
    sections.push({ agent: AgentCode.AgentCodex, rows: rows.filter((r): r is SettingRow => !!r) });
  }
  return first ? [...sections.filter((s) => s.agent === first), ...sections.filter((s) => s.agent !== first)] : sections;
}

// hookText 是一个 hook 写成一行：事件 (匹配): 做什么。和更新计划风险里的写法一样。
export function hookText(h: { event: string; matcher?: string; detail?: string }): string {
  return `${h.event}${h.matcher ? ` (${h.matcher})` : ""}${h.detail ? `: ${h.detail}` : ""}`;
}

// toolText 是 Codex 要接入的一个工具写成一行：名字 (种类): 启动命令或地址。
export function toolText(tool: { type: string; value: string; command?: string; url?: string }): string {
  const target = tool.command || tool.url;
  return `${tool.value} (${tool.type})${target ? `: ${target}` : ""}`;
}

const products: Record<string, string> = { chatgpt: "ChatGPT", codex: "Codex", atlas: "Atlas" };
const productNames = (list: string[] | null | undefined) => messages().agentSettings.list((list ?? []).map((p) => products[p] ?? p));

// permissionNote 是入库预览一行上写的权限：Claude Code 里不用确认就能用的工具、注册的 hooks,Codex 要接入的工具;都没有时为 undefined。
// 行上写各有几个，悬停逐项列出。
export function permissionNote(settings: AgentSettings | null | undefined): Note | undefined {
  const t = messages().agentSettings;
  const allowed = settings?.claude_code?.allowed_tools ?? [];
  const hooks = settings?.claude_code?.hooks ?? [];
  const tools = settings?.codex?.tools ?? [];
  if (!allowed.length && !hooks.length && !tools.length) return undefined;
  const lines = [
    allowed.length > 0 && `${t.claudeAllowedTitle}\n${allowed.map((x) => `  ${x}`).join("\n")}`,
    hooks.length > 0 && `${t.claudeHooksTitle}\n${hooks.map((h) => `  ${hookText(h)}`).join("\n")}`,
    tools.length > 0 && `${t.codexToolsTitle}\n${tools.map((x) => `  ${toolText(x)}`).join("\n")}`,
  ].filter((x): x is string => !!x);
  return { text: t.permissionShort(allowed.length, hooks.length, tools.length), title: [...lines, t.declared].join("\n") };
}
