// 主界面共用的小部件:Agent 标记、计数、路径末段、诊断列表，以及集合切换。
import type { AgentCode } from "@bindings/internal/library";
import { ListBox, ListRow } from "@/components/ui/list";
import { cn } from "@/lib/utils";

// AgentMark 是 Agent 图标：首字母 + 按代码稳定取色,Agent 再多也不用逐个画。
export function AgentMark({ code, name, className }: { code: AgentCode; name: string; className?: string }) {
  const fixed: Record<string, string> = { "claude-code": "#d97757", codex: "#111827" };
  let hue = 0;
  for (const c of code) hue = (hue * 31 + c.charCodeAt(0)) % 360;
  return (
    <span
      title={name}
      className={cn("inline-grid size-3.5 shrink-0 place-items-center rounded text-[9px] leading-none font-bold text-white", className)}
      style={{ background: fixed[code] ?? `hsl(${hue} 55% 48%)` }}
    >
      {code === "codex" ? "X" : name.slice(0, 1)}
    </span>
  );
}

export function Count({ value, className }: { value: number; className?: string }) {
  return (
    <span
      data-slot="count"
      className={cn("min-w-5 shrink-0 rounded-[5px] bg-black/5 px-[5px] text-center font-mono text-[10.5px] text-muted-foreground", className)}
    >
      {value}
    </span>
  );
}

// TailPath 显示路径的末尾几段：放不下时整段省掉开头，写成 …/上级目录/末段，不在目录名中间截断。
export function TailPath({ path, max, className }: { path: string; max: number; className?: string }) {
  return (
    <span title={path} className={cn("min-w-0 truncate", className)}>
      {tailPath(path, max)}
    </span>
  );
}

export function tailPath(path: string, max: number): string {
  if (path.length <= max) return path;
  const parts = path.split("/");
  let tail = parts.pop() ?? "";
  while (parts.length && parts[parts.length - 1].length + tail.length + 3 <= max) tail = `${parts.pop()}/${tail}`;
  return `…/${tail}`;
}

// Diagnostics 折叠列出未能成为候选 / 无法安装的条目及原因。
export function Diagnostics({ summary, items }: { summary: string; items: { key: string; reason: string }[] }) {
  if (!items.length) return null;
  return (
    <details>
      <summary className="cursor-pointer text-[11px] text-muted-foreground">{summary}</summary>
      <ListBox className="mt-1.5">
        {items.map((d) => (
          <ListRow key={d.key}>
            <span className="font-mono text-[11px]">{d.key}</span>
            <span className="grow" />
            <span className="text-[11px] text-muted-foreground">{d.reason}</span>
          </ListRow>
        ))}
      </ListBox>
    </details>
  );
}

export function toggled<T>(set: Set<T>, value: T): Set<T> {
  const next = new Set(set);
  if (!next.delete(value)) next.add(value);
  return next;
}

// Chip 是带 × 的小胶囊(技能所属的组、组安装到的作用域),点 × 移除。
export function Chip({
  children,
  onRemove,
  removeTitle,
  small = false,
}: {
  children: React.ReactNode;
  onRemove: () => void;
  removeTitle: string;
  small?: boolean;
}) {
  return (
    <span
      data-slot="chip"
      className={cn(
        "inline-flex h-6 items-center gap-1 rounded-full border border-border bg-white px-[9px] text-[11.5px] whitespace-nowrap text-foreground-secondary",
        small && "h-5 pr-[7px] pl-[3px] text-[11px]",
      )}
    >
      {children}
      <button type="button" className="text-[13px] leading-none text-subtle hover:text-foreground" title={removeTitle} onClick={onRemove}>
        ×
      </button>
    </span>
  );
}
