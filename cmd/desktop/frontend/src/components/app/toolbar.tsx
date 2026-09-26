// 统一工具栏：左 = 侧栏开关 + 当前筛选;中 = 作用域胶囊;右 = 搜索与视图控制 + 右栏开关。
// 整条是窗口拖动区，能点的元素在 app.css 里显式 no-drag。
import { useEffect, useRef } from "react";
import type { AgentCode } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  agentsUsedIn,
  needsRewrite,
  parseScope,
  projectName,
  projectionsIn,
  scopeKey,
  scopeOf,
  shortPath,
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
  scope: ScopeKey | null;
  scopeDirectory: string | undefined;
  scopeMenu: boolean;
  search: string;
  activeOnly: boolean;
  view: ListView;
  left: boolean;
  right: boolean;
  onScope: (key: ScopeKey | null) => void;
  onScopeMenu: (open: boolean) => void;
  onSearch: (text: string) => void;
  onActiveOnly: (on: boolean) => void;
  onView: (view: ListView) => void;
  onLeft: () => void;
  onRight: () => void;
}

export function Toolbar(props: ToolbarProps) {
  const { snapshot: s, scope } = props;
  const search = useRef<HTMLInputElement>(null);

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
          </span>
        </div>
        <div className="tb-center">
          {s && <ScopePill {...props} s={s} open={props.scopeMenu} />}
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
              title={`只看在 ${agentName(s, parseScope(scope).agent)} 这个作用域已激活的技能`}
            >
              <button
                type="button"
                className={`switch ${props.activeOnly ? "on" : ""}`}
                role="switch"
                aria-checked={props.activeOnly}
                onClick={() => props.onActiveOnly(!props.activeOnly)}
              >
                <i />
              </button>
              <span>仅已激活</span>
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
          <button type="button" className={`icon-btn ${props.right ? "on" : ""}`} onClick={props.onRight} title="显示/隐藏作用域栏">
            <Icon name="sidebarR" />
          </button>
        </div>
      </header>
      {s && props.scopeMenu && <ScopeMenu s={s} {...props} />}
    </>
  );
}

// ScopePill 是顶栏中间的作用域胶囊：未选时是虚线占位，选中后显示 Agent · 地方，点开是选择弹层,× 清除。
function ScopePill({ s, scope, open, scopeDirectory, onScope, onScopeMenu }: ToolbarProps & { s: Snapshot; open: boolean }) {
  if (!scope) {
    return (
      <button type="button" className={`scope-pill empty ${open ? "open" : ""}`} onClick={() => onScopeMenu(!open)}>
        <Icon name="globe" />
        <span>选择作用域</span>
        <Icon name="down" />
      </button>
    );
  }
  const sc = parseScope(scope);
  const issues = projectionsIn(s, scope).filter(needsRewrite).length;
  return (
    <div className={`scope-pill ${open ? "open" : ""}`} title={scopeDirectory ? shortPath(s, scopeDirectory) : undefined}>
      <button type="button" className="scope-pill-main" onClick={() => onScopeMenu(!open)}>
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

// ScopeMenu 是作用域弹层：按地方分组，只列有投影的 Agent,其余 Agent 用每处一个下拉补上。
function ScopeMenu({ s, scope, scopeDirectory, onScope, onScopeMenu }: ToolbarProps & { s: Snapshot }) {
  const places: { id: Place; name: string; path: string }[] = [
    { id: 0, name: "全局", path: "~" },
    ...s.projects.map((p) => ({ id: p.id, name: projectName(s, p.id), path: shortPath(s, p.path) })),
  ];
  const pick = (key: ScopeKey) => {
    onScope(key);
    onScopeMenu(false);
  };
  return (
    <>
      <div className="menu-scrim" onClick={() => onScopeMenu(false)} />
      <div className="scope-menu">
        {places.map((place) => {
          const used = agentsUsedIn(s, place.id);
          const rest = s.agents.filter((a) => !used.includes(a));
          const key = (code: AgentCode) => scopeKey(scopeOf(code, place.id));
          return (
            <div key={place.id}>
              <div className="sm-place">
                <span>
                  <Icon name={place.id ? "folder" : "globe"} />
                  {place.name}
                </span>
                <span className="mono tiny muted">{place.path}</span>
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
                  <Count value={projectionsIn(s, key(a.code)).length} />
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
              onClick={() => {
                onScope(null);
                onScopeMenu(false);
              }}
            >
              清除
            </button>
          </div>
        )}
      </div>
    </>
  );
}
