// 主界面共用的小部件：Agent 标记、计数、诊断列表，以及集合切换。
import type { AgentCode } from "@bindings/internal/library";

// AgentMark 是 Agent 图标：首字母 + 按代码稳定取色，Agent 再多也不用逐个画。
export function AgentMark({ code, name }: { code: AgentCode; name: string }) {
  const fixed: Record<string, string> = { "claude-code": "#d97757", codex: "#111827" };
  let hue = 0;
  for (const c of code) hue = (hue * 31 + c.charCodeAt(0)) % 360;
  return (
    <span className="agent-mark" title={name} style={{ background: fixed[code] ?? `hsl(${hue} 55% 48%)` }}>
      {code === "codex" ? "X" : name.slice(0, 1)}
    </span>
  );
}

// AgentIcon 是侧栏树里的单线条 Agent 图标(与原型一致);没有专属图标的 Agent 用描边方框 + 首字母。
const agentGlyphs: Record<string, React.ReactNode> = {
  "claude-code": <path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6L5.6 18.4" />,
  codex: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2.5" />
      <path d="M7.5 9l3 3-3 3M13 15h4" />
    </>
  ),
  cursor: <path d="M5 3l14 8.5-6.2 1.6L11 19.5z" />,
  gemini: <path d="M12 3c.6 5 4 8.4 9 9-5 .6-8.4 4-9 9-.6-5-4-8.4-9-9 5-.6 8.4-4 9-9z" />,
};

export function AgentIcon({ code, name }: { code: AgentCode; name: string }) {
  return (
    <svg className="agent-icon" viewBox="0 0 24 24" aria-label={name}>
      {agentGlyphs[code] ?? (
        <>
          <rect x="3" y="3" width="18" height="18" rx="4" />
          <text x="12" y="16.5" textAnchor="middle">
            {name.slice(0, 1).toUpperCase()}
          </text>
        </>
      )}
    </svg>
  );
}

export function Count({ value }: { value: number }) {
  return <span className="count">{value}</span>;
}

// Diagnostics 折叠列出未能成为候选 / 无法安装的条目及原因。
export function Diagnostics({ summary, items }: { summary: string; items: { key: string; reason: string }[] }) {
  if (!items.length) return null;
  return (
    <details>
      <summary className="tiny muted">{summary}</summary>
      <div className="list-box" style={{ marginTop: 6 }}>
        {items.map((d) => (
          <div key={d.key} className="list-row">
            <span className="mono tiny">{d.key}</span>
            <span className="grow" />
            <span className="tiny muted">{d.reason}</span>
          </div>
        ))}
      </div>
    </details>
  );
}

export function toggled<T>(set: Set<T>, value: T): Set<T> {
  const next = new Set(set);
  if (!next.delete(value)) next.add(value);
  return next;
}
