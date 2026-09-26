// 主界面共用的小部件：Agent 标记、开关、计数。
import type { AgentCode } from "@bindings/internal/library";

// LATER 是尚未实现的写操作控件的统一提示。
export const LATER = "后续版本提供";

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

// Switch 是原型的开关外观;骨架阶段激活开关只展示状态，一律禁用。
export function Switch({ on, title }: { on: boolean; title?: string }) {
  return (
    <button type="button" className={`switch ${on ? "on" : ""}`} role="switch" aria-checked={on} disabled title={title}>
      <i />
    </button>
  );
}

export function Count({ value, id }: { value: number; id?: string }) {
  return (
    <span className="count" id={id}>
      {value}
    </span>
  );
}
