// 主界面共用的小部件:Agent 标记、计数、顶栏的忙碌标记、路径末段、截断文字的浮动提示、诊断列表，以及集合切换。
import { useRef, useState } from "react";
import type { AgentCode } from "@bindings/internal/library";
import { ListBox, ListRow } from "@/components/ui/list";
import { Spinner } from "@/components/ui/spinner";
import { useMarkStatus } from "@/components/ui/status-mark";
import { useCheckMark } from "@/features/update/check";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentName, projectFolderNote, projectNamed, type Place } from "@/lib/model";
import { useBusyMarkRunning, useSettled } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { agentBrand } from "./agent-icons";

// AgentMark 是 Agent 图标：白底方块里画按名字认出的品牌图标;认不出时画机器人，按代码稳定取色,Agent 再多也分得开。
// 还没有代码(添加 Agent 时的预览)且认不出时用灰色，保存后才有颜色。
export function AgentMark({ code, name, className }: { code?: AgentCode; name: string; className?: string }) {
  const brand = agentBrand(name);
  let hue = 0;
  for (const c of code ?? "") hue = (hue * 31 + c.charCodeAt(0)) % 360;
  return (
    <span
      title={name}
      className={cn("inline-grid size-3.5 shrink-0 place-items-center overflow-hidden rounded bg-white", className)}
      style={{ color: brand ? ("color" in brand ? brand.color : undefined) : code ? `hsl(${hue} 55% 45%)` : "var(--color-subtle)" }}
    >
      {!brand ? (
        <Bot />
      ) : "src" in brand ? (
        <img src={brand.src} alt="" className="pointer-events-none size-[78%]" />
      ) : (
        <brand.Icon aria-hidden className="pointer-events-none size-[78%]" />
      )}
    </span>
  );
}

// Bot 是认不出的 Agent 的机器人图标。不用 Icon:导航项、作用域按钮会给 data-slot=icon 改颜色和尺寸。
function Bot() {
  return (
    <svg
      aria-hidden
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="pointer-events-none size-[78%]"
    >
      <path d="M12 8V4H8" />
      <rect x="4" y="8" width="16" height="12" rx="2" />
      <path d="M2 14h2M20 14h2M15 13v2M9 13v2" />
    </svg>
  );
}

// PlaceAgentMark 是 Agent 在某个地方的图标：项目里共用一个目录合成的一处，叠着画那几个 Agent 的图标(最多 3 个)。
export function PlaceAgentMark({ code, place, className }: { code: AgentCode; place: Place; className?: string }) {
  const s = useSnapshot();
  const named = place ? projectNamed(s, code) : [];
  if (!named.length) return <AgentMark code={code} name={agentName(s, code)} className={className} />;
  return (
    <span className="inline-flex shrink-0 -space-x-1" title={projectFolderNote(s, code, place)}>
      {named.slice(0, 3).map((a) => (
        <AgentMark key={a.code} code={a.code} name={a.name} className={cn("ring-[1.5px] ring-white", className)} />
      ))}
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

// TailPath 显示路径的末尾几段：放不下时整段省掉开头，写成 …/上级目录/末段(Windows 的路径用反斜杠),不在目录名中间截断。
export function TailPath({ path, max, className }: { path: string; max: number; className?: string }) {
  return (
    <span title={path} className={cn("min-w-0 truncate", className)}>
      {tailPath(path, max)}
    </span>
  );
}

export function tailPath(path: string, max: number): string {
  if (path.length <= max) return path;
  const sep = path.includes("\\") ? "\\" : "/";
  const parts = path.split(sep);
  let tail = parts.pop() ?? "";
  while (parts.length && parts[parts.length - 1].length + tail.length + 3 <= max) tail = `${parts.pop()}${sep}${tail}`;
  return `…${sep}${tail}`;
}

// Truncated 给截断显示的文字挂浮动提示：悬停时它自己或里面带 truncate 的元素放不下、被截断了才显示 tip,没截断时不显示;
// always 时总是显示(如 TailPath 已经省掉了开头)。children 是触发的元素，要能接 ref 和事件。
export function Truncated({ tip, always = false, children }: { tip: React.ReactNode; always?: boolean; children: React.ReactElement }) {
  const ref = useRef<HTMLElement>(null);
  const [open, setOpen] = useState(false);
  const truncated = () => {
    const el = ref.current;
    if (!el) return false;
    return [el, ...el.querySelectorAll<HTMLElement>(".truncate")].some((x) => x.scrollWidth > x.clientWidth);
  };
  return (
    <Tooltip open={open} onOpenChange={(next) => setOpen(next && (always || truncated()))}>
      {/* asChild 时 ref 落在 children 上，不一定是 button。 */}
      <TooltipTrigger asChild ref={ref as React.Ref<HTMLButtonElement>}>
        {children}
      </TooltipTrigger>
      <TooltipContent>{tip}</TooltipContent>
    </Tooltip>
  );
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

// BusyMark 是顶栏标题旁的状态圈(主界面与设置页):有写操作进行中时转圈(ui.run 传了 busyMark: false 的除外)，
// 全部结束后转动的弧合成整圈、画出对勾(有失败的画叉),停一下再淡出;只有被取消的写操作时直接消失。
// checks 为 true 时(左栏不显示)也画检查更新的进度，检查本来画在左栏上。
export function BusyMark({ checks = false }: { checks?: boolean }) {
  const writes = useMarkStatus(useBusyMarkRunning(), useSettled());
  const check = useCheckMark();
  const t = useT();
  const shown = checks ? check : null;
  const status = writes === "running" || shown === "running" ? "running" : (writes ?? shown);
  if (!status) return null;
  const busy = status === "running";
  return (
    <Spinner
      status={status}
      title={busy ? t.common.working : undefined}
      className={cn("text-muted-foreground", !busy && "animate-[fade-in_0.2s_ease-out_0.85s_reverse_both]")}
    />
  );
}
