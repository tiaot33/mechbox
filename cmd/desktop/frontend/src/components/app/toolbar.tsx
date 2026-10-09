// 统一工具栏：左 = 侧栏开关 + 当前筛选;中 = 作用域胶囊;右 = 搜索与视图控制 + 右栏开关。
// 整条是窗口拖动区，能点的元素在 app.css 里显式 no-drag。
import { useEffect, useRef, useState } from "react";
import type { AgentCode } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  agentShown,
  agentsUsedIn,
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
import { Icon } from "./icons";
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
    <>
      <header className="titlebar" style={{ "--wails-draggable": "drag" } as React.CSSProperties}>
        <div className="tb-left">
          <div className="tl-space" />
          <button type="button" className={`icon-btn ${props.left ? "on" : ""}`} onClick={props.onLeft} title="显示/隐藏侧栏">
            <Icon name="sidebarL" />
          </button>
          <span className="tb-crumb">
            <b>{props.title}</b>
            {props.count !== undefined && <Count value={props.count} />}
            {props.busy && <span className="spin muted" title="正在处理…" />}
          </span>
        </div>
        <div className="tb-center">
          {s && <ScopePill {...props} s={s} open={menu} onOpen={setMenu} />}
        </div>
        <div className="tb-right">
          <label className="search tb-search">
            <Icon name="search" />
            <input
              ref={search}
              placeholder="搜索技能"
              value={props.search}
              onChange={(e) => props.onSearch(e.target.value)}
              onKeyDown={(e) => e.key === "Escape" && props.onSearch("")}
            />
            <kbd>⌘K</kbd>
          </label>
          {s && scope && (
            <label
              className="toggle-label"
              title={`只看已安装在 ${agentName(s, parseScope(scope).agent)} 这个作用域的技能`}
            >
              <button
                type="button"
                className={`switch ${props.installedOnly ? "on" : ""}`}
                role="switch"
                aria-checked={props.installedOnly}
                onClick={() => props.onInstalledOnly(!props.installedOnly)}
              >
                <i />
              </button>
              <span>仅已安装</span>
            </label>
          )}
          <div className="seg icon-seg">
            <button type="button" className={props.view === "list" ? "on" : ""} onClick={() => props.onView("list")} title="列表">
              <Icon name="list" />
            </button>
            <button type="button" className={props.view === "card" ? "on" : ""} onClick={() => props.onView("card")} title="卡片">
              <Icon name="grid" />
            </button>
          </div>
          <button type="button" className="icon-btn" onClick={props.onRefresh} title="刷新(重新读取技能库并对照磁盘巡检已安装的技能)">
            <Icon name="refresh" />
          </button>
          <button type="button" className={`icon-btn ${props.right ? "on" : ""}`} onClick={props.onRight} title="显示/隐藏作用域栏">
            <Icon name="sidebarR" />
          </button>
        </div>
      </header>
      {s && menu && <ScopeMenu {...props} s={s} onOpen={setMenu} />}
    </>
  );
}

// ScopePill 是顶栏中间的作用域胶囊：未选时是虚线占位，选中后显示 Agent · 地方，点开是选择弹层,× 清除。
type MenuProps = ToolbarProps & { s: Snapshot; onOpen: (open: boolean) => void };

function ScopePill({ s, scope, open, scopeDirectory, onScope, onOpen }: MenuProps & { open: boolean }) {
  if (!scope) {
    return (
      <button type="button" className={`scope-pill empty ${open ? "open" : ""}`} onClick={() => onOpen(!open)}>
        <Icon name="globe" />
        <span>选择作用域</span>
        <Icon name="down" />
      </button>
    );
  }
  const sc = parseScope(scope);
  const issues = skillScopesIn(s, scope).filter(needsReinstall).length;
  return (
    <div className={`scope-pill ${open ? "open" : ""}`} title={scopeDirectory ? shortPath(s, scopeDirectory) : undefined}>
      <button type="button" className="scope-pill-main" onClick={() => onOpen(!open)}>
        <AgentMark code={sc.agent} name={agentName(s, sc.agent)} />
        <b>{agentName(s, sc.agent)}</b>
        <span className="muted">· {sc.project_id ? projectName(s, sc.project_id) : "全局"}</span>
        {issues > 0 && <span className="status warn">{issues}</span>}
        <Icon name="down" />
      </button>
      <button type="button" className="scope-pill-clear" onClick={() => onScope(null)} title="取消选择作用域">
        <Icon name="close" />
      </button>
    </div>
  );
}

// ScopeMenu 是作用域弹层：按地方分组，只列装有技能的 Agent,其余勾选显示的 Agent 用每处一个下拉补上。
function ScopeMenu({ s, scope, scopeDirectory, onScope, onOpen }: MenuProps) {
  const places: { id: Place; name: string; path: string }[] = [
    { id: 0, name: "全局", path: "~" },
    ...s.projects.map((p) => ({ id: p.id, name: projectName(s, p.id), path: shortPath(s, p.path) })),
  ];
  const pick = (key: ScopeKey | null) => {
    onScope(key);
    onOpen(false);
  };
  return (
    <>
      <div className="menu-scrim" onClick={() => onOpen(false)} />
      <div className="scope-menu">
        {places.map((place) => {
          const used = agentsUsedIn(s, place.id);
          const rest = s.agents.filter((a) => !used.includes(a) && agentShown(s.agentsShown, a));
          const key = (code: AgentCode) => scopeKey(scopeOf(code, place.id));
          return (
            <div key={place.id}>
              <div className="sm-place">
                <span>
                  <Icon name={place.id ? "folder" : "globe"} />
                  {place.name}
                </span>
                {/* 名字已是路径末段，放不下时截掉末尾、保留父目录，用来区分同名项目。 */}
                <span className="mono tiny muted sm-path" title={place.path}>
                  {place.path}
                </span>
              </div>
              {used.map((a) => (
                <button
                  type="button"
                  key={a.code}
                  className={`sm-item ${scope === key(a.code) ? "on" : ""}`}
                  onClick={() => pick(key(a.code))}
                >
                  <AgentMark code={a.code} name={a.name} />
                  <span className="grow">{a.name}</span>
                  <Count value={skillScopesIn(s, key(a.code)).length} />
                </button>
              ))}
              {rest.length > 0 && (
                <select className="sm-more" value="" onChange={(e) => e.target.value && pick(e.target.value)}>
                  <option value="">+ 其他 {rest.length} 个 Agent</option>
                  {rest.map((a) => (
                    <option key={a.code} value={key(a.code)}>
                      {a.name}
                    </option>
                  ))}
                </select>
              )}
            </div>
          );
        })}
        {scope && (
          <div className="sm-foot">
            <span className="mono tiny muted">{scopeDirectory ? shortPath(s, scopeDirectory) : ""}</span>
            <button
              type="button"
              className="link"
              onClick={() => pick(null)}
            >
              清除
            </button>
          </div>
        )}
      </div>
    </>
  );
}
