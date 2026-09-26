// 主界面：从技能出发的三栏布局(UI 原型变体 A)——左栏筛选、中间技能卡片、右栏作用域，顶部统一工具栏;
// 点卡片打开右侧详情抽屉。写操作经 lib/actions 与 lib/ui 的 run 执行，完成后重读快照。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import { FilterSidebar } from "@/components/app/filter-sidebar";
import { InstallDialog } from "@/components/app/install-dialog";
import { ScopePanel } from "@/components/app/scope-panel";
import { SkillDrawer } from "@/components/app/skill-drawer";
import { SkillList } from "@/components/app/skill-list";
import { Toolbar, type ListView } from "@/components/app/toolbar";
import { activateGroup, activateSkill, addSkillToGroup } from "@/lib/actions";
import { DnDContext, type DnD, type DragItem } from "@/lib/dnd";
import { useLibrarySnapshot, useScopeDirectory, type LibraryError, type Snapshot } from "@/lib/library";
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
import { UIProvider, useUI } from "@/lib/ui";
import "./app.css";

function toggled<T>(set: Set<T>, value: T): Set<T> {
  const next = new Set(set);
  if (!next.delete(value)) next.add(value);
  return next;
}

export default function App() {
  const [state, reload] = useLibrarySnapshot();
  return (
    <UIProvider reload={reload}>
      <Main state={state} reload={reload} />
    </UIProvider>
  );
}

function Main({ state, reload }: { state: ReturnType<typeof useLibrarySnapshot>[0]; reload: () => void }) {
  const ui = useUI();
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
  const [drawer, setDrawer] = useState<number | null>(null);
  const [drag, setDrag] = useState<DragItem | null>(null);
  const [config, setConfig] = useState<ConfigStatus>({ sync: false, store_url: false, github_token: false });
  const ghost = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const call = LibraryService.Config();
    call.then(setConfig, () => {});
    return () => void call.cancel();
  }, []);

  const s: Snapshot | undefined = state.status === "ready" ? state.snapshot : undefined;
  const scopeDirectory = useScopeDirectory(scope ? parseScope(scope) : null);
  const current = s ? resolveFilter(s, filter) : filter;
  const skills = s ? visibleSkills(s, { filter: current, search, activeOnlyIn: activeOnly ? scope : null }) : [];
  const drawerSkill = s && drawer !== null ? s.skills.find((x) => x.id === drawer) : undefined;

  // 作用域或项目被移除(注销项目、拉取了别的库)后，界面上的选择退回。
  useEffect(() => {
    if (!s || !scope) return;
    const sc = parseScope(scope);
    if (sc.project_id && !s.projects.some((p) => p.id === sc.project_id)) setScope(null);
  }, [s, scope]);

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

  const home = s?.info.home_dir ?? "";
  const openInstall = useCallback(() => {
    void ui.open((close) => <InstallDialog config={config} home={home} onClose={close} />);
  }, [ui, config, home]);

  const dnd: DnD = useMemo(
    () => ({
      item: drag,
      start: (e, item, label) => {
        e.dataTransfer.effectAllowed = "copy";
        e.dataTransfer.setData("text/plain", `${item.kind}:${item.id}`);
        if (ghost.current) {
          ghost.current.textContent = label;
          e.dataTransfer.setDragImage(ghost.current, 12, 12);
        }
        // 下一帧再改状态，避免重渲染打断浏览器的拖动开始。
        window.setTimeout(() => {
          setDrag(item);
          setRight(true);
        });
      },
      end: () => setDrag(null),
      dropOnScope: (key) => {
        if (!s || !drag) return;
        const sc = parseScope(key);
        setDrag(null);
        void (drag.kind === "skill" ? activateSkill(ui, s, drag.id, sc) : activateGroup(ui, s, drag.id, sc));
      },
      dropOnGroup: (id) => {
        if (!s || drag?.kind !== "skill") return;
        setDrag(null);
        void addSkillToGroup(ui, s, id, drag.id);
      },
    }),
    [drag, s, ui],
  );

  return (
    <DnDContext.Provider value={dnd}>
      <div className="window">
        <Toolbar
          snapshot={s}
          title={s ? filterTitle(s, current) : "技能管理器"}
          count={s ? skills.length : undefined}
          busy={ui.busy}
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
          onRefresh={reload}
        />
        <div className={`a-body ${drag ? `dragging drag-${drag.kind}` : ""}`}>
          {state.status === "loading" && <main className="a-center tile" />}
          {state.status === "error" && <LoadError error={state.error} onRetry={reload} />}
          {s && (
            <>
              {left && <FilterSidebar s={s} config={config} filter={current} onFilter={setFilter} />}
              <SkillList
                s={s}
                skills={skills}
                filter={current}
                scope={scope}
                scopeDirectory={scopeDirectory}
                view={view}
                selected={drawer}
                onOpen={(id) => setDrawer(drawer === id ? null : id)}
                onInstall={openInstall}
              />
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
              {drawerSkill && <SkillDrawer s={s} skill={drawerSkill} onClose={() => setDrawer(null)} />}
            </>
          )}
        </div>
        <div ref={ghost} className="drag-ghost" />
      </div>
    </DnDContext.Provider>
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
