// 统一工具栏：左 = 侧栏开关 + 当前筛选;中 = 作用域胶囊;右 = 搜索与视图控制 + 右栏开关。
// 整条是窗口拖动区，能点的元素显式 no-drag(见 layout.ts 的 titlebarClass)。
import { useEffect, useRef, useState } from "react";
import type { AgentCode } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  agentsIn,
  needsReinstall,
  parseScope,
  projectName,
  scopeKey,
  scopeOf,
  shortPath,
  skillScopesIn,
  type Place,
  type ScopeKey,
} from "@/lib/model";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Segmented } from "@/components/ui/segmented";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Icon } from "./icons";
import { titlebarClass } from "./layout";
import { AgentMark, Count } from "./parts";

export type ListView = "card" | "list";

interface ToolbarProps {
  snapshot: Snapshot | undefined;
  title: string;
  count: number | undefined;
  busy: boolean;
  scope: ScopeKey | null;
  scopeDirectory: string | undefined;
  search: string;
  installedOnly: boolean;
  view: ListView;
  left: boolean;
  right: boolean;
  onScope: (key: ScopeKey | null) => void;
  onSearch: (text: string) => void;
  onInstalledOnly: (on: boolean) => void;
  onView: (view: ListView) => void;
  onLeft: () => void;
  onRight: () => void;
  onRefresh: () => void;
}

export function Toolbar(props: ToolbarProps) {
  const { snapshot: s, scope } = props;
  const search = useRef<HTMLInputElement>(null);
  const [menu, setMenu] = useState(false);

  // ⌘K 聚焦搜索框。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        search.current?.focus();
        search.current?.select();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <header className={titlebarClass} style={{ "--wails-draggable": "drag" } as React.CSSProperties}>
      <div className="flex min-w-0 items-center gap-2">
        <div className="w-16" />
        <Button variant="ghost" size="icon" className={cn(props.left && "text-foreground")} onClick={props.onLeft} title="显示/隐藏侧栏">
          <Icon name="sidebarL" />
        </Button>
        <span className="tb-crumb ml-1 inline-flex items-center gap-1.5 text-[13px] font-semibold whitespace-nowrap">
          <b>{props.title}</b>
          {props.count !== undefined && <Count value={props.count} />}
          {props.busy && <Spinner className="text-muted-foreground" title="正在处理…" />}
        </span>
      </div>
      <div className="flex justify-center">
        {s && (
          <Popover open={menu} onOpenChange={setMenu}>
            <ScopePill {...props} s={s} open={menu} />
            <ScopeMenu {...props} s={s} onOpen={setMenu} />
          </Popover>
        )}
      </div>
      <div className="flex min-w-0 items-center justify-end gap-2">
        <SearchInput
          ref={search}
          className="h-[26px] min-w-[110px] flex-[0_1_240px] px-[7px] text-[11.5px]"
          icon={<Icon name="search" className="size-[13px] text-muted-foreground" />}
          suffix={<kbd className="rounded border border-border px-1 font-sans text-[10.5px] text-subtle">⌘K</kbd>}
          placeholder="搜索技能"
          value={props.search}
          onChange={(e) => props.onSearch(e.target.value)}
          onKeyDown={(e) => e.key === "Escape" && props.onSearch("")}
        />
        {s && scope && (
          <label
            className="toggle-label inline-flex shrink-0 items-center gap-1.5 text-xs whitespace-nowrap text-foreground-secondary"
            title={`只看已安装在 ${agentName(s, parseScope(scope).agent)} 这个作用域的技能`}
          >
            <Switch checked={props.installedOnly} onCheckedChange={props.onInstalledOnly} />
            <span>仅已安装</span>
          </label>
        )}
        <Segmented
          className="icon-seg"
          iconOnly
          value={props.view}
          onChange={props.onView}
          options={[
            { value: "list", label: <Icon name="list" />, title: "列表" },
            { value: "card", label: <Icon name="grid" />, title: "卡片" },
          ]}
        />
        <Button variant="ghost" size="icon" onClick={props.onRefresh} title="刷新(重新读取技能库并对照磁盘巡检已安装的技能)">
          <Icon name="refresh" />
        </Button>
        <Button variant="ghost" size="icon" className={cn(props.right && "text-foreground")} onClick={props.onRight} title="显示/隐藏作用域栏">
          <Icon name="sidebarR" />
        </Button>
      </div>
    </header>
  );
}

// ScopePill 是顶栏中间的作用域胶囊：未选时是淡色占位，选中后显示 Agent · 地方，点开是选择弹层,× 清除。
type MenuProps = ToolbarProps & { s: Snapshot };

const pillClass =
  "scope-pill inline-flex h-7 items-center rounded-full border border-border bg-white text-xs font-medium whitespace-nowrap shadow-[0_1px_2px_rgba(0,0,0,0.04)] hover:border-primary-border data-[open=true]:border-primary-border [&_[data-slot=icon]]:size-[11px] [&_[data-slot=icon]]:text-subtle";

function ScopePill({ s, scope, open, scopeDirectory, onScope }: MenuProps & { open: boolean }) {
  if (!scope) {
    return (
      <PopoverTrigger asChild>
        <button type="button" className={cn(pillClass, "gap-1.5 bg-white/60 px-3 text-muted-foreground")} data-open={open}>
          <Icon name="globe" />
          <span>选择作用域</span>
          <Icon name="down" />
        </button>
      </PopoverTrigger>
    );
  }
  const sc = parseScope(scope);
  const issues = skillScopesIn(s, scope).filter(needsReinstall).length;
  return (
    <PopoverAnchor asChild>
      <div className={pillClass} data-open={open} data-no-drag title={scopeDirectory ? shortPath(s, scopeDirectory) : undefined}>
        <PopoverTrigger asChild>
          <button type="button" className="inline-flex h-full items-center gap-1.5 pr-2 pl-2.5">
            <AgentMark code={sc.agent} name={agentName(s, sc.agent)} />
            <b>{agentName(s, sc.agent)}</b>
            <span className="text-muted-foreground">· {sc.project_id ? projectName(s, sc.project_id) : "全局"}</span>
            {issues > 0 && <Badge tone="warn">{issues}</Badge>}
            <Icon name="down" />
          </button>
        </PopoverTrigger>
        <button
          type="button"
          className="scope-pill-clear grid h-full place-items-center border-l border-border-subtle pr-2.5 pl-1.5 hover:[&_[data-slot=icon]]:text-foreground"
          onClick={() => onScope(null)}
          title="取消选择作用域"
        >
          <Icon name="close" />
        </button>
      </div>
    </PopoverAnchor>
  );
}

// ScopeMenu 是作用域弹层：按地方分组，和右栏一样直接列出装有技能的与检测到的 Agent,
// 勾选了显示但没检测到的用每处一个下拉补上。
function ScopeMenu({ s, scope, scopeDirectory, onScope, onOpen }: MenuProps & { onOpen: (open: boolean) => void }) {
  const places: { id: Place; name: string; path: string }[] = [
    { id: 0, name: "全局", path: "~" },
    ...s.projects.map((p) => ({ id: p.id, name: projectName(s, p.id), path: shortPath(s, p.path) })),
  ];
  const pick = (key: ScopeKey | null) => {
    onScope(key);
    onOpen(false);
  };
  return (
    <PopoverContent className="scope-menu max-h-[70vh] w-80 overflow-y-auto">
      {places.map((place) => {
        const { open: used, folded: rest } = agentsIn(s, place.id);
        const key = (code: AgentCode) => scopeKey(scopeOf(code, place.id));
        return (
          <div key={place.id}>
            <div className="sm-place flex items-center gap-3 px-2 pt-2 pb-1 text-[11.5px] font-semibold text-muted-foreground">
              <span className="inline-flex min-w-0 items-center gap-[5px] whitespace-nowrap">
                <Icon name={place.id ? "folder" : "globe"} className="size-3" />
                {place.name}
              </span>
              {/* 名字已是路径末段，放不下时截掉末尾、保留父目录，用来区分同名项目。 */}
              <span className="min-w-0 flex-1 truncate text-right font-mono text-[11px] font-normal" title={place.path}>
                {place.path}
              </span>
            </div>
            {used.map((a) => (
              <button
                type="button"
                key={a.code}
                className={cn(
                  "sm-item flex h-[30px] w-full items-center gap-2 rounded-[7px] pr-2.5 pl-[22px] text-left hover:bg-black/5",
                  scope === key(a.code) && "bg-primary-soft font-semibold text-primary hover:bg-primary-soft",
                )}
                onClick={() => pick(key(a.code))}
              >
                <AgentMark code={a.code} name={a.name} />
                <span className="grow">{a.name}</span>
                <Count value={skillScopesIn(s, key(a.code)).length} />
              </button>
            ))}
            {rest.length > 0 && (
              <NativeSelect
                className="sm-more mt-0.5 mb-1 ml-[22px] h-[26px] max-w-none text-muted-foreground"
                value=""
                onChange={(e) => e.target.value && pick(e.target.value)}
              >
                <option value="">+ {used.length ? "其他 " : ""}{rest.length} 个 Agent</option>
                {rest.map((a) => (
                  <option key={a.code} value={key(a.code)}>
                    {a.name}
                  </option>
                ))}
              </NativeSelect>
            )}
          </div>
        );
      })}
      {scope && (
        <div className="mt-1.5 flex items-center justify-between gap-2 border-t border-border-subtle px-2.5 pt-2 pb-1">
          <span className="truncate font-mono text-[11px] text-muted-foreground">{scopeDirectory ? shortPath(s, scopeDirectory) : ""}</span>
          <Button variant="link" onClick={() => pick(null)}>
            清除
          </Button>
        </div>
      )}
    </PopoverContent>
  );
}
