// 主界面：从技能出发的三栏布局(UI 原型变体 A)——左栏筛选、中间技能卡片、右栏作用域，顶部统一工具栏。
// 骨架只读：写操作相关的控件照原型摆出但禁用。
import { useState } from "react";
import { FilterSidebar } from "@/components/app/filter-sidebar";
import { ScopePanel } from "@/components/app/scope-panel";
import { SkillList } from "@/components/app/skill-list";
import { Toolbar, type ListView } from "@/components/app/toolbar";
import { useLibrarySnapshot, useScopeDirectory, type LibraryError } from "@/lib/library";
import {
  agentsUsedIn,
  filterTitle,
  parseScope,
  placeOf,
  resolveFilter,
  visibleSkills,
  type Place,
  type ScopeKey,
  type SkillFilter,
} from "@/lib/model";
import "./app.css";

function toggled<T>(set: Set<T>, value: T): Set<T> {
  const next = new Set(set);
  if (!next.delete(value)) next.add(value);
  return next;
}

export default function App() {
  const [state, reload] = useLibrarySnapshot();
  const [filter, setFilter] = useState<SkillFilter>({ kind: "all" });
  const [scope, setScope] = useState<ScopeKey | null>(null);
  const [scopeMenu, setScopeMenu] = useState(false);
  const [search, setSearch] = useState("");
  const [activeOnly, setActiveOnly] = useState(false);
  const [view, setView] = useState<ListView>("card");
  const [left, setLeft] = useState(true);
  const [right, setRight] = useState(true);
  const [expanded, setExpanded] = useState<Set<Place>>(new Set());
  const [moreAgents, setMoreAgents] = useState<Set<Place>>(new Set());

  const s = state.status === "ready" ? state.snapshot : undefined;
  const scopeDirectory = useScopeDirectory(scope ? parseScope(scope) : null);
  const current = s ? resolveFilter(s, filter) : filter;
  const skills = s ? visibleSkills(s, { filter: current, search, activeOnlyIn: activeOnly ? scope : null }) : [];

  // 选中作用域时，右栏同步展开到那一行，否则折叠的项目或"其他 Agent"里看不到选中态。
  const selectScope = (key: ScopeKey | null) => {
    setScope(key);
    if (!key) {
      setActiveOnly(false);
      return;
    }
    const sc = parseScope(key);
    const place = placeOf(sc);
    if (place) setExpanded((set) => new Set(set).add(place));
    if (s && !agentsUsedIn(s, place).some((a) => a.code === sc.agent)) setMoreAgents((set) => new Set(set).add(place));
  };

  return (
    <div className="window">
      <Toolbar
        snapshot={s}
        title={s ? filterTitle(s, current) : "技能管理器"}
        count={s ? skills.length : undefined}
        scope={scope}
        scopeDirectory={scopeDirectory}
        scopeMenu={scopeMenu}
        search={search}
        activeOnly={activeOnly}
        view={view}
        left={left}
        right={right}
        onScope={selectScope}
        onScopeMenu={setScopeMenu}
        onSearch={setSearch}
        onActiveOnly={setActiveOnly}
        onView={setView}
        onLeft={() => setLeft(!left)}
        onRight={() => setRight(!right)}
      />
      <div className="a-body">
        {state.status === "loading" && <main className="a-center tile" />}
        {state.status === "error" && <LoadError error={state.error} onRetry={reload} />}
        {s && (
          <>
            {left && <FilterSidebar s={s} filter={current} onFilter={setFilter} />}
            <SkillList s={s} skills={skills} filter={current} scope={scope} scopeDirectory={scopeDirectory} view={view} />
            {right && (
              <ScopePanel
                s={s}
                scope={scope}
                expanded={expanded}
                moreAgents={moreAgents}
                onScope={selectScope}
                onToggleProject={(id) => setExpanded((set) => toggled(set, id))}
                onToggleMore={(place) => setMoreAgents((set) => toggled(set, place))}
              />
            )}
          </>
        )}
      </div>
    </div>
  );
}

function LoadError({ error, onRetry }: { error: LibraryError; onRetry: () => void }) {
  return (
    <main className="a-center tile">
      <div className="list-empty">
        <b>读取技能库失败</b>
        <span style={{ userSelect: "text" }}>{error.message}</span>
        <button type="button" className="pill-btn" onClick={onRetry}>
          重试
        </button>
      </div>
    </main>
  );
}
