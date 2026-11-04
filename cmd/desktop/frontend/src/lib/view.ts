// 主界面的界面状态：在哪一页(技能库 / 更新 / 作用域 / 项目)、技能库页按什么分节、搜索、左右栏、右栏的展开、
// 列表里收起的节与展开的"没装的"、勾选的技能、左栏要跳到的那一节、详情抽屉、拖放、集合与组的先后与设置页。
// 各栏直接从这里取、改，不经 App 一层层传;改状态的操作是模块级函数，组件外(拖放、写操作流程)也能调用。
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { Collection, CollectionID, Group, GroupID, ProjectID, Scope, SkillID } from "@bindings/internal/library";
import { queryClient, scopeDirectoryQuery, snapshotQuery, useLoadedSnapshot, type Snapshot } from "./library";
import {
  agentsIn,
  moved,
  noOrder,
  pageListing,
  pageSkills,
  pickRange,
  parseScope,
  prunedOrder,
  placeOf,
  resolvePage,
  samePage,
  sectionKey,
  sortedCollections,
  sortedGroups,
  type Arrange,
  type GraphSet,
  type Order,
  type Page,
  type PageListing,
  type PickRow,
  type Place,
  type ScopeKey,
} from "./model";
import { toggled } from "./utils";

// DragItem 是正在拖的东西：技能行、勾选的几个技能(拖的是勾选着的一行)、组(左栏组行、组的节标题)或集合(集合的节标题)。
export type DragItem =
  | { kind: "skill"; id: SkillID }
  | { kind: "skills"; ids: SkillID[] }
  | { kind: "group"; id: GroupID }
  | { kind: "collection"; id: CollectionID };

// Jump 是左栏点了集合或组之后要滚到的那一节;n 每次点都变，连点同一个也会再滚一次。
export interface Jump {
  section: string;
  n: number;
}

// Flash 是刚入库的未纳管技能：它在作用域页、项目页里换成库里技能的那一行，滚到看得见的地方闪一下。
// scope 是入库的那个作用域，同一个技能在页上有几行时闪这一处的;n 每次入库都变。
export interface Flash {
  skill: SkillID;
  scope: ScopeKey;
  n: number;
}

interface ViewState {
  page: Page;
  arrange: Arrange;
  search: string;
  left: boolean;
  right: boolean;
  expanded: ReadonlySet<Place>; // 右栏展开的项目
  moreAgents: ReadonlySet<Place>; // 右栏展开了"其他 Agent"的地方
  flipped: ReadonlySet<string>; // 列表里收起、展开和默认相反的节(默认展开的被收起了，默认收起的被展开了)
  unfolded: ReadonlySet<string>; // 列表里展开了"没装的"的节，以及展开了下面那块(没装在这里的)的作用域页(`${作用域}:not-here`)
  picked: ReadonlySet<SkillID>; // 列表里勾选的技能，底部的操作栏对它们一起操作;换页时清空
  anchor: string | null; // 上一次单独勾选或取消的那一行(PickRow.key),按住 Shift 勾选时从它勾到点的那一行
  jump: Jump | null;
  flash: Flash | null;
  drawer: SkillID | null;
  drag: DragItem | null;
  order: Order; // 用户拖出来的集合与组的先后，存在 localStorage,下次打开沿用
  settings: boolean;
  settingsSection: string | null; // 打开设置页时直接跳到的分区
  back: Back | null; // 关系图页返回到哪一页、那一页滚到了哪里
  restoreScroll: number | null; // 从关系图页返回后，列表要滚回去的位置;滚过去之后清掉
}

// Back 是打开关系图之前所在的页面、列表滚到的位置、搜索与勾选，返回时都回到那时的样子。
export interface Back {
  page: Page;
  scroll: number;
  search: string;
  picked: ReadonlySet<SkillID>;
  anchor: string | null;
}

const orderKey = "mechbox.order";

function savedOrder(): Order {
  try {
    const v = JSON.parse(localStorage.getItem(orderKey) ?? "null");
    const ids = (x: unknown) => (Array.isArray(x) ? x.filter((id) => typeof id === "number") : []);
    return v ? { collections: ids(v.collections), groups: ids(v.groups) } : noOrder;
  } catch {
    return noOrder;
  }
}

export const useView = create<ViewState>()(() => ({
  page: { kind: "all" },
  arrange: "collection",
  search: "",
  left: true,
  right: true,
  expanded: new Set(),
  moreAgents: new Set(),
  flipped: new Set(),
  unfolded: new Set(),
  picked: new Set(),
  anchor: null,
  jump: null,
  flash: null,
  drawer: null,
  drag: null,
  order: savedOrder(),
  settings: false,
  settingsSection: null,
  back: null,
  restoreScroll: null,
}));

const set = useView.setState;
const get = useView.getState;

export const setSearch = (search: string) => set({ search });
export const setArrange = (arrange: Arrange) => set({ arrange });
export const toggleLeft = () => set((v) => ({ left: !v.left }));
export const toggleRight = () => set((v) => ({ right: !v.right }));
export const toggleProject = (place: Place) => set((v) => ({ expanded: toggled(v.expanded, place) }));
export const toggleMoreAgents = (place: Place) => set((v) => ({ moreAgents: toggled(v.moreAgents, place) }));
export const toggleSection = (key: string) => set((v) => ({ flipped: toggled(v.flipped, key) }));
export const toggleUnfolded = (key: string) => set((v) => ({ unfolded: toggled(v.unfolded, key) }));
export const openSettings = (section: string | null = null) => set({ settings: true, settingsSection: section });
export const closeSettings = () => set({ settings: false });

// toggleDrawer 打开技能详情;点的是当前打开的那个技能时关上。
export const toggleDrawer = (id: SkillID) => set((v) => ({ drawer: v.drawer === id ? null : id }));
export const closeDrawer = () => set({ drawer: null });

// openPage 换到技能库或更新页;左栏上次要跳到的那一节作废，回到技能库页时不再滚过去。
export const openPage = (page: Page) => goTo(page);

// goTo 换页：勾选的技能只对当前页有效，换到别的页时清空;左栏要跳到的那一节、要闪的那一行作废。
function goTo(page: Page, extra: Partial<ViewState> = {}) {
  const changed = !samePage(get().page, page);
  set({ page, jump: null, flash: null, ...(changed ? { picked: new Set(), anchor: null } : {}), ...extra });
}

// openGraph 打开集合或组的关系图页(节标题上的「关系图」、左栏右键菜单);记下现在在哪一页、列表滚到了哪里、搜索与勾选，
// 返回时回到那里。从关系图页再打开别的关系图，返回的仍是最初那一页。
export function openGraph(set: GraphSet) {
  const { page, back, search, picked, anchor } = get();
  const from = page.kind === "graph" ? back : { page, scroll: document.querySelector<HTMLElement>(".a-list")?.scrollTop ?? 0, search, picked, anchor };
  goTo({ kind: "graph", set }, { back: from });
}

// closeGraph 从关系图页回到打开它之前的那一页：列表滚回原来的位置，搜索与勾选也恢复;这期间移除了的、那一页不再列出的技能不再勾着。
export function closeGraph() {
  const { back } = get();
  if (!back) return goTo({ kind: "all" }, { back: null });
  goTo(back.page, { back: null, restoreScroll: back.scroll, search: back.search });
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  const listed = s ? pageSkills(s, resolvePage(s, back.page)) : new Set<SkillID>();
  set({ picked: new Set([...back.picked].filter((id) => listed.has(id))), anchor: back.anchor });
}

export const doneRestoreScroll = () => set({ restoreScroll: null });

// flashSkill 让刚入库的未纳管技能那一行(这时已是库里技能的一行)滚到看得见的地方闪一下;doneFlash 是闪过了，之后再渲染不再闪。
export const flashSkill = (skill: SkillID, scope: ScopeKey) => set((v) => ({ flash: { skill, scope, n: (v.flash?.n ?? 0) + 1 } }));
export const doneFlash = (n: number) => set((v) => (v.flash?.n === n ? { flash: null } : {}));

// togglePick 勾选或取消一行的技能。rows 是按住 Shift 点的：列表里各行从上到下，
// 从上一次单独勾选的那一行到这一行都设成和这一行点完后一样的状态;上一次的那一行已经不在列表里时只改这一行。
export function togglePick(row: PickRow, rows?: PickRow[]) {
  const { picked, anchor } = get();
  const on = !picked.has(row.id);
  const range = rows && anchor !== null ? pickRange(rows, anchor, row.key) : [];
  if (range.length) setPicked(range, on, anchor);
  else setPicked([row.id], on, row.key);
}

// setPicked 把几个技能一起勾上或取消(节标题上的勾选框)。
export function setPicked(ids: SkillID[], on: boolean, anchor: string | null = get().anchor) {
  set((v) => {
    const next = new Set(v.picked);
    for (const id of ids) {
      if (on) next.add(id);
      else next.delete(id);
    }
    return { picked: next, anchor };
  });
}

export const clearPicked = () => set({ picked: new Set(), anchor: null });

// openScope 打开作用域页，右栏同步展开到那一行;再点一次正打开的作用域回到技能库。
export function openScope(key: ScopeKey) {
  const { page } = get();
  if (page.kind === "scope" && page.scope === key) return goTo({ kind: "all" });
  goTo({ kind: "scope", scope: key });
  reveal(parseScope(key));
}

// openProject 打开项目页(项目里各 Agent 装了什么),右栏同步展开这个项目;再点一次正打开的项目回到技能库。
export function openProject(project: ProjectID) {
  const { page } = get();
  if (page.kind === "project" && page.project === project) return goTo({ kind: "all" });
  goTo({ kind: "project", project }, { expanded: new Set(get().expanded).add(project) });
}

// jumpTo 是左栏点了集合或组：回到技能库页、按它分节、清掉搜索，展开并滚到那一节。
export function jumpTo(target: { kind: "collection"; id: CollectionID } | { kind: "group"; id: GroupID }) {
  const section = sectionKey(target.kind, target.id);
  const v = get();
  goTo(
    { kind: "all" },
    {
      arrange: target.kind,
      search: "",
      flipped: new Set([...v.flipped].filter((k) => k !== section)),
      jump: { section, n: (v.jump?.n ?? 0) + 1 },
    },
  );
}

// reveal 把右栏展开到作用域那一行，否则折叠的项目或"其他 Agent"里看不到它。
export function reveal(scope: Scope) {
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  const place = placeOf(scope);
  set((v) => ({
    expanded: place ? new Set(v.expanded).add(place) : v.expanded,
    moreAgents: s && !agentsIn(s, place).open.some((a) => a.code === scope.agent) ? new Set(v.moreAgents).add(place) : v.moreAgents,
  }));
}

// startDrag / endDrag 记下正在拖的东西;开始拖时打开右栏，好放到作用域上。
export const startDrag = (item: DragItem) => set({ drag: item, right: true });
export const endDrag = () => set({ drag: null });

// reorder 把拖动的集合或组挪到另一个(target)的前面或后面(after),左栏与列表的分节一起变，记在这台机器上。
export function reorder(kind: "collection" | "group", dragged: number, target: number, after: boolean) {
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  if (!s) return;
  const { order } = get();
  const next =
    kind === "collection"
      ? { ...order, collections: moved(sortedCollections(s, order.collections).map((c) => c.id), dragged, target, after) }
      : { ...order, groups: moved(sortedGroups(s, order.groups).map((g) => g.id), dragged, target, after) };
  saveOrder(next);
}

// resetOrder 把集合或组恢复成默认的先后(左栏节标题的右键菜单)。
export function resetOrder(kind: "collection" | "group") {
  const { order } = get();
  saveOrder(kind === "collection" ? { ...order, collections: [] } : { ...order, groups: [] });
}

// forgetOrder 清掉拖出来的先后：拉取了别的库之后，旧库的 ID 套不到新库的集合与组上。
export const forgetOrder = () => saveOrder(noOrder);

function saveOrder(order: Order) {
  localStorage.setItem(orderKey, JSON.stringify(order));
  set({ order });
}

// useSortedCollections / useSortedGroups 是按用户拖出来的先后排好的集合与组;要列出它们的地方都用这两个，和左栏一致。
export function useSortedCollections(s: Snapshot): Collection[] {
  const order = useView((v) => v.order.collections);
  return useMemo(() => sortedCollections(s, order), [s, order]);
}

export function useSortedGroups(s: Snapshot): Group[] {
  const order = useView((v) => v.order.groups);
  return useMemo(() => sortedGroups(s, order), [s, order]);
}

// reconcile 在快照变了之后校正在哪一页：更新都处理完了、作用域的项目或 Agent 被移除(注销项目、删除自定义 Agent、
// 拉取了别的库)时退回技能库，并记下来，之后又有了更新也不会自己跳回去。
// 拖出来的先后里已经不在库里的集合与组也在这时去掉;勾选的技能只留这一页还列着的(移除了的、项目页上卸载了的去掉),
// 免得看不见的技能被带进下一次操作。
export function reconcile(s: Snapshot) {
  const { page, order, picked } = get();
  const resolved = resolvePage(s, page);
  if (!samePage(resolved, page)) set({ page: resolved, picked: new Set(), anchor: null });
  else if (picked.size) {
    const listed = pageSkills(s, resolved);
    if ([...picked].some((id) => !listed.has(id))) set({ picked: new Set([...picked].filter((id) => listed.has(id))) });
  }
  const pruned = prunedOrder(s, order);
  if (pruned !== order) saveOrder(pruned);
}

// useCurrentPage 是校正后的页面(reconcile 随后把它记下来)。
export function useCurrentPage(): Page {
  const s = useLoadedSnapshot();
  const page = useView((v) => v.page);
  return s ? resolvePage(s, page) : page;
}

// usePageListing 是当前页要列的东西与个数(见 pageListing),列表、顶栏和搜索行都读它。
export function usePageListing(): PageListing | undefined {
  const s = useLoadedSnapshot();
  const page = useCurrentPage();
  const { arrange, search, order } = useView(useShallow((v) => ({ arrange: v.arrange, search: v.search, order: v.order })));
  return useMemo(() => (s ? pageListing(s, page, arrange, search, order) : undefined), [s, page, arrange, search, order]);
}

// useScopeDirectory 是作用域页的 Agent 目录;不在作用域页或还没读到时为 undefined。
export function useScopeDirectory(): string | undefined {
  const page = useCurrentPage();
  const scope = page.kind === "scope" ? page.scope : null;
  const home = useLoadedSnapshot()?.info.home_dir;
  return useQuery(scopeDirectoryQuery(scope, home)).data;
}
