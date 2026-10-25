// 主界面的界面状态：筛选、作用域、搜索、"仅已安装"、视图、左右栏、右栏的展开、详情抽屉、拖放与设置页。
// 各栏直接从这里取、改，不经 App 一层层传;改状态的操作是模块级函数，组件外(拖放、写操作流程)也能调用。
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { GroupID, Scope, SkillID } from "@bindings/internal/library";
import { queryClient, scopeDirectoryQuery, snapshotQuery, useLoadedSnapshot, type Snapshot } from "./library";
import {
  agentsIn,
  parseScope,
  placeOf,
  resolveFilter,
  sameFilter,
  visibleSkills,
  visibleUnmanaged,
  type Place,
  type ScopeKey,
  type SkillFilter,
} from "./model";
import { toggled } from "./utils";

export type ListView = "card" | "list";

// DragItem 是正在拖的东西：技能卡片，或组(左栏组行、列表顶部的组标题条)。
export type DragItem = { kind: "skill"; id: SkillID } | { kind: "group"; id: GroupID };

interface ViewState {
  filter: SkillFilter;
  scope: ScopeKey | null;
  search: string;
  installedOnly: boolean;
  view: ListView;
  left: boolean;
  right: boolean;
  expanded: ReadonlySet<Place>; // 右栏展开的项目
  moreAgents: ReadonlySet<Place>; // 右栏展开了"其他 Agent"的地方
  drawer: SkillID | null;
  drag: DragItem | null;
  settings: boolean;
  settingsSection: string | null; // 打开设置页时直接跳到的分区
}

export const useView = create<ViewState>()(() => ({
  filter: { kind: "all" },
  scope: null,
  search: "",
  installedOnly: false,
  view: "card",
  left: true,
  right: true,
  expanded: new Set(),
  moreAgents: new Set(),
  drawer: null,
  drag: null,
  settings: false,
  settingsSection: null,
}));

const set = useView.setState;
const get = useView.getState;

export const setFilter = (filter: SkillFilter) => set({ filter });
export const setSearch = (search: string) => set({ search });
export const setInstalledOnly = (installedOnly: boolean) => set({ installedOnly });
export const setView = (view: ListView) => set({ view });
export const toggleLeft = () => set((v) => ({ left: !v.left }));
export const toggleRight = () => set((v) => ({ right: !v.right }));
export const toggleProject = (place: Place) => set((v) => ({ expanded: toggled(v.expanded, place) }));
export const toggleMoreAgents = (place: Place) => set((v) => ({ moreAgents: toggled(v.moreAgents, place) }));
export const openSettings = (section: string | null = null) => set({ settings: true, settingsSection: section });
export const closeSettings = () => set({ settings: false });

// toggleDrawer 打开技能详情;点的是当前打开的那个技能时关上。
export const toggleDrawer = (id: SkillID) => set((v) => ({ drawer: v.drawer === id ? null : id }));
export const closeDrawer = () => set({ drawer: null });

// reveal 把右栏展开到作用域那一行，否则折叠的项目或"其他 Agent"里看不到它。
export function reveal(scope: Scope) {
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  const place = placeOf(scope);
  set((v) => ({
    expanded: place ? new Set(v.expanded).add(place) : v.expanded,
    moreAgents: s && !agentsIn(s, place).open.some((a) => a.code === scope.agent) ? new Set(v.moreAgents).add(place) : v.moreAgents,
  }));
}

// selectScope 选中作用域(顶栏胶囊与弹层),右栏同步展开到那一行;清除时一并关掉"仅已安装"。
export function selectScope(key: ScopeKey | null) {
  set(key ? { scope: key } : { scope: null, installedOnly: false });
  if (key) reveal(parseScope(key));
}

// pickScope 是在右栏点选作用域：选中并按它筛选列表;关掉顶栏"仅已安装"可回到全部技能继续安装。
export function pickScope(key: ScopeKey | null) {
  selectScope(key);
  if (key) set({ installedOnly: true });
}

// startDrag / endDrag 记下正在拖的东西;开始拖时打开右栏，好放到作用域上。
export const startDrag = (item: DragItem) => set({ drag: item, right: true });
export const endDrag = () => set({ drag: null });

// reconcile 在快照变了之后校正界面上的选择：筛选指向的集合、组已不存在或已没有更新时退回"所有技能",并记下来，
// 之后又有了更新也不会自己跳回去;作用域的项目或 Agent 被移除(注销项目、删除自定义 Agent、拉取了别的库)后清除作用域。
export function reconcile(s: Snapshot) {
  const { filter, scope } = get();
  const resolved = resolveFilter(s, filter);
  if (!sameFilter(resolved, filter)) set({ filter: resolved });
  if (!scope) return;
  const sc = parseScope(scope);
  if ((sc.project_id && !s.projects.some((p) => p.id === sc.project_id)) || !s.agents.some((a) => a.code === sc.agent)) set({ scope: null });
}

// useCurrentFilter 是校正后的筛选：指向的集合、组已不存在或已没有更新时是"所有技能"(reconcile 随后把它记下来)。
export function useCurrentFilter(): SkillFilter {
  const s = useLoadedSnapshot();
  const filter = useView((v) => v.filter);
  return s ? resolveFilter(s, filter) : filter;
}

// useListing 是中间列表要列的东西：校正后的筛选、技能与未纳管技能。顶栏的标题、计数和列表用同一份。
export function useListing() {
  const s = useLoadedSnapshot();
  const { filter, search, installedOnly, scope } = useView(
    useShallow((v) => ({ filter: v.filter, search: v.search, installedOnly: v.installedOnly, scope: v.scope })),
  );
  return useMemo(() => {
    if (!s) return { filter, skills: [], unmanaged: [] };
    const current = resolveFilter(s, filter);
    return {
      filter: current,
      skills: visibleSkills(s, { filter: current, search, installedOnlyIn: installedOnly ? scope : null }),
      unmanaged: visibleUnmanaged(s, current, search, scope),
    };
  }, [s, filter, search, installedOnly, scope]);
}

// useScopeDirectory 是选中的作用域对应的 Agent 目录;没选作用域或还没读到时为 undefined。
export function useScopeDirectory(): string | undefined {
  const scope = useView((v) => v.scope);
  const home = useLoadedSnapshot()?.info.home_dir;
  return useQuery(scopeDirectoryQuery(scope, home)).data;
}
