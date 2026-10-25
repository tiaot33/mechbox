// 主界面的界面状态：在哪一页(所有技能 / 更新 / 作用域)、所有技能页按什么分节、搜索、左右栏、右栏的展开、
// 列表里收起的节与展开的"没装的"、左栏要跳到的那一节、详情抽屉、拖放与设置页。
// 各栏直接从这里取、改，不经 App 一层层传;改状态的操作是模块级函数，组件外(拖放、写操作流程)也能调用。
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { CollectionID, GroupID, Scope, SkillID } from "@bindings/internal/library";
import { queryClient, scopeDirectoryQuery, snapshotQuery, useLoadedSnapshot, type Snapshot } from "./library";
import {
  agentsIn,
  pageListing,
  parseScope,
  placeOf,
  resolvePage,
  samePage,
  sectionKey,
  type Arrange,
  type Page,
  type PageListing,
  type Place,
  type ScopeKey,
} from "./model";
import { toggled } from "./utils";

// DragItem 是正在拖的东西：技能行、组(左栏组行、组的节标题)或集合(集合的节标题)。
export type DragItem = { kind: "skill"; id: SkillID } | { kind: "group"; id: GroupID } | { kind: "collection"; id: CollectionID };

// Jump 是左栏点了集合或组之后要滚到的那一节;n 每次点都变，连点同一个也会再滚一次。
export interface Jump {
  section: string;
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
  unfolded: ReadonlySet<string>; // 列表里展开了"没装的"的节
  jump: Jump | null;
  drawer: SkillID | null;
  drag: DragItem | null;
  settings: boolean;
  settingsSection: string | null; // 打开设置页时直接跳到的分区
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
  jump: null,
  drawer: null,
  drag: null,
  settings: false,
  settingsSection: null,
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

// openPage 换到所有技能或更新页;左栏上次要跳到的那一节作废，回到所有技能页时不再滚过去。
export const openPage = (page: Page) => set({ page, jump: null });

// openScope 打开作用域页，右栏同步展开到那一行;再点一次正打开的作用域回到所有技能。
export function openScope(key: ScopeKey) {
  const { page } = get();
  if (page.kind === "scope" && page.scope === key) return set({ page: { kind: "all" }, jump: null });
  set({ page: { kind: "scope", scope: key }, jump: null });
  reveal(parseScope(key));
}

// jumpTo 是左栏点了集合或组：回到所有技能页、按它分节、清掉搜索，展开并滚到那一节。
export function jumpTo(target: { kind: "collection"; id: CollectionID } | { kind: "group"; id: GroupID }) {
  const section = sectionKey(target.kind, target.id);
  set((v) => ({
    page: { kind: "all" },
    arrange: target.kind,
    search: "",
    flipped: new Set([...v.flipped].filter((k) => k !== section)),
    jump: { section, n: (v.jump?.n ?? 0) + 1 },
  }));
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

// reconcile 在快照变了之后校正在哪一页：更新都处理完了、作用域的项目或 Agent 被移除(注销项目、删除自定义 Agent、
// 拉取了别的库)时退回所有技能，并记下来，之后又有了更新也不会自己跳回去。
export function reconcile(s: Snapshot) {
  const { page } = get();
  const resolved = resolvePage(s, page);
  if (!samePage(resolved, page)) set({ page: resolved });
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
  const { arrange, search } = useView(useShallow((v) => ({ arrange: v.arrange, search: v.search })));
  return useMemo(() => (s ? pageListing(s, page, arrange, search) : undefined), [s, page, arrange, search]);
}

// useScopeDirectory 是作用域页的 Agent 目录;不在作用域页或还没读到时为 undefined。
export function useScopeDirectory(): string | undefined {
  const page = useCurrentPage();
  const scope = page.kind === "scope" ? page.scope : null;
  const home = useLoadedSnapshot()?.info.home_dir;
  return useQuery(scopeDirectoryQuery(scope, home)).data;
}
