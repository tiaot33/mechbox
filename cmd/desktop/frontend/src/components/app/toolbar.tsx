// 统一工具栏：左 = 侧栏开关 + 当前筛选;中 = 作用域胶囊;右 = 搜索与视图控制 + 右栏开关。
// 整条是窗口拖动区，能点的元素显式 no-drag(见 layout.ts 的 titlebarClass)。
import { useEffect, useRef, useState } from "react";
import type { AgentCode } from "@bindings/internal/library";
import { useShallow } from "zustand/react/shallow";
import { useT } from "@/lib/i18n";
import { useSnapshot, useLoadedSnapshot, reloadLibrary } from "@/lib/library";
import {
  agentNameIn,
  agentsIn,
  needsReinstall,
  parseScope,
  placeOf,
  projectFolderNote,
  projectName,
  sameMergedScope,
  scopeKey,
  scopeOf,
  shortPath,
  skillScopesIn,
  type Place,
  type ScopeKey,
  filterTitle,
} from "@/lib/model";
import { useBusy } from "@/lib/ui";
import { cn } from "@/lib/utils";
import {
  selectScope,
  setInstalledOnly,
  setSearch,
  setView,
  toggleLeft,
  toggleRight,
  useListing,
  useScopeDirectory,
  useView,
} from "@/lib/view";
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
import { Count, PlaceAgentMark } from "./parts";

export function Toolbar() {
  const s = useLoadedSnapshot();
  const busy = useBusy();
  const listing = useListing();
  const { scope, search: text, installedOnly, view, left, right } = useView(
    useShallow((v) => ({ scope: v.scope, search: v.search, installedOnly: v.installedOnly, view: v.view, left: v.left, right: v.right })),
  );
  const search = useRef<HTMLInputElement>(null);
  const [menu, setMenu] = useState(false);
  const tt = useT();
  const t = tt.toolbar;

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
        <Button variant="ghost" size="icon" className={cn(left && "text-foreground")} onClick={toggleLeft} title={t.toggleLeft}>
          <Icon name="sidebarL" />
        </Button>
        <span className="tb-crumb ml-1 inline-flex items-center gap-1.5 text-[13px] font-semibold whitespace-nowrap">
          <b>{s ? filterTitle(s, listing.filter) : "Mechbox"}</b>
          {s && <Count value={listing.skills.length + listing.unmanaged.length} />}
          {busy && <Spinner className="text-muted-foreground" title={tt.common.working} />}
        </span>
      </div>
      <div className="flex justify-center">
        {s && (
          <Popover open={menu} onOpenChange={setMenu}>
            <ScopePill open={menu} />
            <ScopeMenu onOpen={setMenu} />
          </Popover>
        )}
      </div>
      <div className="flex min-w-0 items-center justify-end gap-2">
        <SearchInput
          ref={search}
          className="h-[26px] min-w-[110px] flex-[0_1_240px] px-[7px] text-[11.5px]"
          icon={<Icon name="search" className="size-[13px] text-muted-foreground" />}
          suffix={<kbd className="rounded border border-border px-1 font-sans text-[10.5px] text-subtle">⌘K</kbd>}
          placeholder={t.search}
          value={text}
          onChange={(e) => setSearch(e.target.value)}
          onKeyDown={(e) => e.key === "Escape" && setSearch("")}
        />
        {s && scope && (
          <label
            className="toggle-label inline-flex shrink-0 items-center gap-1.5 text-xs whitespace-nowrap text-foreground-secondary"
            title={t.installedOnlyTitle(agentNameIn(s, parseScope(scope).agent, placeOf(parseScope(scope))))}
          >
            <Switch checked={installedOnly} onCheckedChange={setInstalledOnly} />
            <span>{t.installedOnly}</span>
          </label>
        )}
        <Segmented
          className="icon-seg"
          iconOnly
          value={view}
          onChange={setView}
          options={[
            { value: "list", label: <Icon name="list" />, title: t.list },
            { value: "card", label: <Icon name="grid" />, title: t.card },
          ]}
        />
        <Button variant="ghost" size="icon" onClick={() => void reloadLibrary()} title={t.refresh}>
          <Icon name="refresh" />
        </Button>
        <Button variant="ghost" size="icon" className={cn(right && "text-foreground")} onClick={toggleRight} title={t.toggleRight}>
          <Icon name="sidebarR" />
        </Button>
      </div>
    </header>
  );
}

// ScopePill 是顶栏中间的作用域胶囊：未选时是淡色占位，选中后显示 Agent · 地方，点开是选择弹层,× 清除。
const pillClass =
  "scope-pill inline-flex h-7 items-center rounded-full border border-border bg-white text-xs font-medium whitespace-nowrap shadow-[0_1px_2px_rgba(0,0,0,0.04)] hover:border-primary-border data-[open=true]:border-primary-border [&_[data-slot=icon]]:size-[11px] [&_[data-slot=icon]]:text-subtle";

function ScopePill({ open }: { open: boolean }) {
  const scope = useView((v) => v.scope);
  const scopeDirectory = useScopeDirectory();
  const s = useSnapshot();
  const t = useT();
  if (!scope) {
    return (
      <PopoverTrigger asChild>
        <button type="button" className={cn(pillClass, "gap-1.5 bg-white/60 px-3 text-muted-foreground")} data-open={open}>
          <Icon name="globe" />
          <span>{t.toolbar.chooseScope}</span>
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
            <PlaceAgentMark code={sc.agent} place={placeOf(sc)} />
            <b className="max-w-[240px] truncate">{agentNameIn(s, sc.agent, placeOf(sc))}</b>
            <span className="text-muted-foreground">· {sc.project_id ? projectName(s, sc.project_id) : t.model.global}</span>
            {issues > 0 && <Badge tone="warn">{issues}</Badge>}
            <Icon name="down" />
          </button>
        </PopoverTrigger>
        <button
          type="button"
          className="scope-pill-clear grid h-full place-items-center border-l border-border-subtle pr-2.5 pl-1.5 hover:[&_[data-slot=icon]]:text-foreground"
          onClick={() => selectScope(null)}
          title={t.toolbar.clearScope}
        >
          <Icon name="close" />
        </button>
      </div>
    </PopoverAnchor>
  );
}

// ScopeMenu 是作用域弹层：按地方分组，和右栏一样直接列出装有技能的与检测到的 Agent,
// 勾选了显示但没检测到的用每处一个下拉补上。
function ScopeMenu({ onOpen }: { onOpen: (open: boolean) => void }) {
  const scope = useView((v) => v.scope);
  const scopeDirectory = useScopeDirectory();
  const s = useSnapshot();
  const t = useT();
  const places: { id: Place; name: string; path: string }[] = [
    { id: 0, name: t.model.global, path: "~" },
    ...s.projects.map((p) => ({ id: p.id, name: projectName(s, p.id), path: shortPath(s, p.path) })),
  ];
  const pick = (key: ScopeKey | null) => {
    selectScope(key);
    onOpen(false);
  };
  return (
    <PopoverContent className="scope-menu max-h-[70vh] w-80 overflow-y-auto">
      {places.map((place) => {
        const { open: used, folded: rest } = agentsIn(s, place.id);
        const key = (code: AgentCode) => scopeKey(scopeOf(code, place.id));
        const active = (code: AgentCode) => !!scope && sameMergedScope(s, parseScope(scope), scopeOf(code, place.id));
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
                  active(a.code) && "bg-primary-soft font-semibold text-primary hover:bg-primary-soft",
                )}
                onClick={() => pick(key(a.code))}
              >
                <PlaceAgentMark code={a.code} place={place.id} />
                <span className="grow truncate" title={projectFolderNote(s, a.code, place.id)}>
                  {agentNameIn(s, a.code, place.id)}
                </span>
                <Count value={skillScopesIn(s, key(a.code)).length} />
              </button>
            ))}
            {rest.length > 0 && (
              <NativeSelect
                className="sm-more mt-0.5 mb-1 ml-[22px] h-[26px] max-w-none text-muted-foreground"
                value=""
                onChange={(e) => e.target.value && pick(e.target.value)}
              >
                <option value="">{t.toolbar.moreAgents(used.length > 0, rest.length)}</option>
                {rest.map((a) => (
                  <option key={a.code} value={key(a.code)}>
                    {agentNameIn(s, a.code, place.id)}
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
            {t.toolbar.clear}
          </Button>
        </div>
      )}
    </PopoverContent>
  );
}
