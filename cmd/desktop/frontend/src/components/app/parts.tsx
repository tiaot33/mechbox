// 主界面共用的小部件：Agent 标记、计数。
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

export function Count({ value, id }: { value: number; id?: string }) {
  return (
    <span className="count" id={id}>
      {value}
    </span>
  );
}
