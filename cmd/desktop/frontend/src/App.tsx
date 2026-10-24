// 主界面：从技能出发的三栏布局(UI 原型变体 A)——左栏筛选、中间技能卡片、右栏作用域，顶部统一工具栏;
// 点卡片打开右侧详情抽屉;左栏"设置"把三栏换成设置页。写操作经 lib/actions 与 lib/ui 的 run 执行，完成后重读快照。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { AppService, LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import type { Scope } from "@bindings/internal/library";
import { FetchDialog } from "@/components/app/fetch-dialog";
import { FilterSidebar } from "@/components/app/filter-sidebar";
import { ScopePanel } from "@/components/app/scope-panel";
import { SettingsPage } from "@/components/app/settings-page";
import { SkillDrawer } from "@/components/app/skill-drawer";
import { toggled } from "@/components/app/parts";
import { SkillList } from "@/components/app/skill-list";
import { Toolbar, type ListView } from "@/components/app/toolbar";
import { offerFirstOpen } from "@/components/app/fetch-unmanaged-dialog";
import { addSkillToGroup, installGroup, installSkill } from "@/lib/actions";
import { DnDContext, type DnD, type DragItem } from "@/lib/dnd";
import { useLibrarySnapshot, useScopeDirectory, type LibraryError, type Snapshot } from "@/lib/library";
import {
  agentsIn,
  filterTitle,
  parseScope,
  placeOf,
  resolveFilter,
  sameFilter,
  visibleSkills,
  visibleUnmanaged,
  type Place,
  type ScopeKey,
  type SkillFilter,
} from "@/lib/model";
import { messages, useT } from "@/lib/i18n";
import { UIProvider, useUI } from "@/lib/ui";
import { Notice } from "@/components/ui/notice";
import { bodyClass, centerClass } from "@/components/app/layout";
import { ResizablePane } from "@/components/app/resizable";
import { Button } from "@/components/ui/button";
import "./index.css";

export default function App() {
  const [state, reload] = useLibrarySnapshot();
  // 读一次当前语言的文案：切换语言时 App 随之重渲染，整个界面(包括组件外按模块级语言取文案的地方)跟着换。
  useT();
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
  const [search, setSearch] = useState("");
  const [installedOnly, setInstalledOnly] = useState(false);
  const [view, setView] = useState<ListView>("card");
  const [left, setLeft] = useState(true);
  const [right, setRight] = useState(true);
  const [expanded, setExpanded] = useState<Set<Place>>(new Set());
  const [moreAgents, setMoreAgents] = useState<Set<Place>>(new Set());
  const [drawer, setDrawer] = useState<number | null>(null);
  const [drag, setDrag] = useState<DragItem | null>(null);
  const [config, setConfig] = useState<ConfigStatus>({ sync: false, github_token: false });
  const [settings, setSettings] = useState(false);
  const ghost = useRef<HTMLDivElement>(null);

  // 设置页保存 GitHub 令牌或同步设置后重读，保存晚于回到主界面完成时也一样。
  const loadConfig = useCallback(() => void LibraryService.Config().then(setConfig, () => {}), []);
  useEffect(loadConfig, [loadConfig]);

  // Go 端自动检查更新后通知重读，窗口关在菜单栏时也一样。
  useEffect(() => Events.On("updates-checked", reload), [reload]);

  const s: Snapshot | undefined = state.status === "ready" ? state.snapshot : undefined;

  // 在这台机器上第一次打开 app 时，把用户带到各 Agent 全局目录里不是库装的技能，一次确认全部入库。
  const firstOpen = useRef(false);
  useEffect(() => {
    if (!s || firstOpen.current) return;
    firstOpen.current = true;
    void offerFirstOpen(ui, s.agents);
  }, [s, ui]);

  // 设置里选的 WSL 发行版启动时没能打开：说明界面上暂时是 Windows 本机的全局目录，让用户再试或改回 Windows 本机。
  const distroChecked = useRef(false);
  useEffect(() => {
    if (!s || distroChecked.current) return;
    distroChecked.current = true;
    void offerDistroRetry(ui);
  }, [s, ui]);

  // 快照变了(写操作、手动检查更新之后),Dock 角标跟着重算。
  useEffect(() => {
    if (s) void AppService.RefreshBadge();
  }, [s]);

  const scopeDirectory = useScopeDirectory(scope, s?.info.home_dir);
  const current = s ? resolveFilter(s, filter) : filter;
  const skills = s ? visibleSkills(s, { filter: current, search, installedOnlyIn: installedOnly ? scope : null }) : [];
  const unmanaged = s ? visibleUnmanaged(s, current, search, scope) : [];
  const drawerSkill = s && drawer !== null ? s.skills.find((x) => x.id === drawer) : undefined;

  // 筛选指向的集合、组已不存在或已没有更新时退回"所有技能",并记下来:之后又有了更新也不会自己跳回去。
  useEffect(() => {
    if (!sameFilter(current, filter)) setFilter(current);
  }, [current, filter]);

  // 作用域、项目或 Agent 被移除(注销项目、删除自定义 Agent、拉取了别的库)后，界面上的选择退回。
  useEffect(() => {
    if (!s || !scope) return;
    const sc = parseScope(scope);
    if (sc.project_id && !s.projects.some((p) => p.id === sc.project_id)) setScope(null);
    if (!s.agents.some((a) => a.code === sc.agent)) setScope(null);
  }, [s, scope]);

  // reveal 把右栏展开到作用域那一行，否则折叠的项目或"其他 Agent"里看不到它。
  const reveal = (sc: Scope) => {
    const place = placeOf(sc);
    if (place) setExpanded((set) => new Set(set).add(place));
    if (s && !agentsIn(s, place).open.some((a) => a.code === sc.agent)) setMoreAgents((set) => new Set(set).add(place));
  };

  // 选中作用域时，右栏同步展开到那一行。
  const selectScope = (key: ScopeKey | null) => {
    setScope(key);
    if (!key) {
      setInstalledOnly(false);
      return;
    }
    reveal(parseScope(key));
  };

  const home = s?.info.home_dir ?? "";
  const openFetch = useCallback(() => {
    void ui.open((close) => <FetchDialog home={home} onClose={close} />);
  }, [ui, home]);

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
        // 拖动中临时展开的地方拖完会收回，放下的那一行保持展开，好看到装上的结果。
        reveal(sc);
        void (drag.kind === "skill" ? installSkill(ui, s, drag.id, sc) : installGroup(ui, s, drag.id, sc));
      },
      dropOnGroup: (id) => {
        if (!s || drag?.kind !== "skill") return;
        setDrag(null);
        void addSkillToGroup(ui, s, id, drag.id);
      },
    }),
    [drag, s, ui],
  );

  if (s && settings) {
    return (
      <div className="window-backdrop flex h-screen flex-col overflow-hidden">
        <SettingsPage
          s={s}
          onClose={() => {
            // 设置页改的 Agent 显示勾选只存进应用设置，回到主界面时重读一次。
            setSettings(false);
            reload();
          }}
          onConfigChange={loadConfig}
        />
      </div>
    );
  }

  return (
    <DnDContext.Provider value={dnd}>
      <div className="window-backdrop relative flex h-screen flex-col overflow-hidden">
        <Toolbar
          snapshot={s}
          title={s ? filterTitle(s, current) : "Mechbox"}
          count={s ? skills.length + unmanaged.length : undefined}
          busy={ui.busy}
          scope={scope}
          scopeDirectory={scopeDirectory}
          search={search}
          installedOnly={installedOnly}
          view={view}
          left={left}
          right={right}
          onScope={selectScope}
          onSearch={setSearch}
          onInstalledOnly={setInstalledOnly}
          onView={setView}
          onLeft={() => setLeft(!left)}
          onRight={() => setRight(!right)}
          onRefresh={reload}
        />
        <div className={bodyClass} data-dragging={drag?.kind}>
          {state.status === "loading" && <main className={centerClass} />}
          {state.status === "error" && <LoadError error={state.error} onRetry={reload} />}
          {s && (
            <>
              {left && (
                <ResizablePane side="left" storageKey="mechbox.left-width" defaultWidth={210} min={180} max={360}>
                  <FilterSidebar
                    s={s}
                    config={config}
                    filter={current}
                    onFilter={setFilter}
                    onFetch={openFetch}
                    onSettings={() => setSettings(true)}
                  />
                </ResizablePane>
              )}
              <SkillList
                s={s}
                skills={skills}
                unmanaged={unmanaged}
                filter={current}
                scope={scope}
                scopeDirectory={scopeDirectory}
                view={view}
                selected={drawer}
                onOpen={(id) => setDrawer(drawer === id ? null : id)}
                onFetch={openFetch}
              />
              {right && (
                <ResizablePane side="right" storageKey="mechbox.right-width" defaultWidth={270} min={220} max={440}>
                <ScopePanel
                  s={s}
                  scope={scope}
                  expanded={expanded}
                  moreAgents={moreAgents}
                  onScope={(key) => {
                    // 在右栏点选作用域即按它筛选列表;关掉顶栏"仅已安装"可回到全部技能继续安装。
                    selectScope(key);
                    if (key) setInstalledOnly(true);
                  }}
                  onToggleProject={(id) => setExpanded((set) => toggled(set, id))}
                  onToggleMore={(place) => setMoreAgents((set) => toggled(set, place))}
                />
                </ResizablePane>
              )}
              {drawerSkill && <SkillDrawer s={s} skill={drawerSkill} onClose={() => setDrawer(null)} />}
            </>
          )}
        </div>
        <div
          ref={ghost}
          className="fixed -top-[200px] left-0 rounded-full bg-primary px-3 py-[5px] text-xs font-semibold whitespace-nowrap text-white shadow-[0_4px_12px_rgba(0,113,227,0.35)]"
        />
      </div>
    </DnDContext.Provider>
  );
}

async function offerDistroRetry(ui: ReturnType<typeof useUI>) {
  const t = messages().app;
  for (;;) {
    const reason = await AppService.WSLDistroError().catch(() => "");
    if (!reason) return;
    const settings = await AppService.Settings();
    const distro = settings.wsl_distro ?? "";
    const choice = await ui.confirm({
      title: t.distroUnavailable(distro),
      body: <Notice tone="warn">{t.distroUnavailableBody(reason)}</Notice>,
      choices: [
        { value: "retry", label: t.retryDistro, desc: t.retryDistroDesc },
        { value: "windows", label: t.useWindowsHome, desc: t.useWindowsHomeDesc },
      ],
      confirm: t.continue,
    });
    if (!choice) {
      ui.toast(t.distroFallback(distro));
      return;
    }
    if (choice === "windows") {
      await ui.run(() => AppService.SaveSettings({ ...settings, wsl_distro: "" }));
      return;
    }
    try {
      await AppService.RetryWSLDistro();
      ui.toast(t.distroOpened(distro));
      ui.reload();
      return;
    } catch {
      // 仍打不开：再给一次选择，原因换成这次的。
    }
  }
}

function LoadError({ error, onRetry }: { error: LibraryError; onRetry: () => void }) {
  const t = useT().app;
  return (
    <main className={centerClass}>
      <div className="flex flex-col items-center gap-1.5 pt-[22vh] text-muted-foreground">
        <b className="text-sm text-foreground">{t.loadFailed}</b>
        <span className="select-text">{error.message}</span>
        <Button className="mt-1.5" onClick={onRetry}>
          {t.retry}
        </Button>
      </div>
    </main>
  );
}
