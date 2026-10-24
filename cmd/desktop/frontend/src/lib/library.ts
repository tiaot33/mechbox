// 桌面端读取技能库的入口：经 Wails 绑定调用 Go 侧的 LibraryService,读到的数据放在 TanStack Query 的缓存里。
// 写操作完成后 reloadLibrary 让缓存失效，界面上正在用的查询随之重读(前端在自己的写操作之后重读，见 ADR 0008)。
import { useSyncExternalStore } from "react";
import { QueryClient, queryOptions, useQuery, type Query } from "@tanstack/react-query";
import { CancelError, type CancellablePromise } from "@wailsio/runtime";
import { AppService, LibraryService, type AppInfo } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  type Error as LibraryErrorModel,
  type Agent,
  type Collection,
  type Group,
  type GroupInstall,
  type RegisteredProject,
  type Skill,
  type SkillScopeInspection,
  type SameNameSkill,
  type PlannedUnmanagedSkill,
  type Scope,
  type SkillID,
  type UnmanagedSkill,
  type UpdateHistoryFilter,
  type VersionID,
} from "@bindings/internal/library";
import { parseScope, scopeKey, type ScopeKey } from "./model";

// LibraryError 就是技能库的 library.Error(类型由绑定生成);Go 端的 MarshalError 把任意错误规范成它，
// Wails 运行时放在被拒绝的 Error 的 cause 上。
export type LibraryError = LibraryErrorModel;

// 前端自己取消的调用(关掉弹窗等)由 Wails 运行时以 CancelError 拒绝，和技能库的 Canceled 一样算正常取消。
export function toLibraryError(err: unknown): LibraryError {
  if (err instanceof CancelError) return { code: ErrorCode.Canceled, message: err.message };
  const cause = err instanceof Error ? err.cause : undefined;
  if (cause && typeof cause === "object" && "code" in cause && "message" in cause) {
    return cause as LibraryError;
  }
  return { code: ErrorCode.Internal, message: err instanceof Error ? err.message : String(err) };
}

// Snapshot 是主界面一次读出的全部只读数据;agentsShown 来自这台机器的应用设置，其余来自技能库。
export interface Snapshot {
  info: AppInfo;
  agents: Agent[];
  agentsShown: Partial<Record<string, boolean>>;
  projects: RegisteredProject[];
  skills: Skill[];
  collections: Collection[];
  groups: Group[];
  groupInstalls: GroupInstall[];
  skillScopes: SkillScopeInspection[];
  unmanaged: UnmanagedSkill[];
  sameName: SameNameSkill[];
}

// ---------- 查询缓存 ----------

// 缓存里的数据只在写操作之后(reloadLibrary)或用户点重试时重读：窗口重新获得焦点不重读，失败也不自动重试，
// 免得 Go 端报的错被悄悄重试掉。
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: Infinity, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false },
  },
});

// stable 标记写操作后不用重读的查询：内容只由 key 决定(某个版本的文件),或者来自技能库以外(skills.sh 的审计)。
declare module "@tanstack/react-query" {
  interface Register {
    queryMeta: { stable?: boolean };
  }
}
const stable = { stable: true };

// reloadLibrary 让查询缓存失效，界面上正在用的查询立即重读，其余的下次用到时再读;重读期间保留旧数据，界面不闪"加载中"。
export function reloadLibrary(): Promise<void> {
  return queryClient.invalidateQueries({ predicate: (q: Query) => !q.meta?.stable });
}

// wails 把绑定返回的可取消调用接到查询的 signal 上：查询被取消(组件卸载、重读)时取消 Go 端的调用。
function wails<T>(signal: AbortSignal, call: CancellablePromise<T>): CancellablePromise<T> {
  signal.addEventListener("abort", () => void call.cancel(), { once: true });
  return call;
}

// Go 的 nil 切片经 JSON 变成 null,这里统一成空数组。
function list<T>(call: Promise<T[] | null>): Promise<T[]> {
  return call.then((value) => value ?? []);
}

// ---------- 快照 ----------

async function readSnapshot(signal: AbortSignal): Promise<Snapshot> {
  const [info, agents, projects, skills, collections, groups, groupInstalls, skillScopes, unmanaged, sameName, settings] = await Promise.all([
    wails(signal, LibraryService.Info()),
    list(wails(signal, LibraryService.ListAgents())),
    list(wails(signal, LibraryService.ListRegisteredProjects())),
    list(wails(signal, LibraryService.ListSkills())),
    list(wails(signal, LibraryService.ListCollections())),
    list(wails(signal, LibraryService.ListGroups())),
    list(wails(signal, LibraryService.ListGroupInstalls())),
    list(wails(signal, LibraryService.InspectSkillScopes({}))),
    list(wails(signal, LibraryService.ListUnmanagedSkills())),
    list(wails(signal, LibraryService.ListSameNameSkills())),
    wails(signal, AppService.Settings()),
  ]);
  findSources(unmanaged);
  const agentsShown = settings.agents_shown ?? {};
  return { info, agents, agentsShown, projects, skills, collections, groups, groupInstalls, skillScopes, unmanaged, sameName };
}

// snapshotQuery 一次读出主界面需要的全部只读数据。各部分互相引用(安装状态里的技能 ID、组成员等),
// 所以整份一起读、一起换，不拆成各自重读的查询。
export const snapshotQuery = queryOptions({ queryKey: ["snapshot"], queryFn: ({ signal }) => readSnapshot(signal) });

// useSnapshotQuery 给主界面区分加载中、读取失败与已读到。
export function useSnapshotQuery() {
  return useQuery(snapshotQuery);
}

// useLoadedSnapshot 是读到的快照;还在第一次读取，或最近一次读取失败(主界面显示原因与重试)时为 undefined。
export function useLoadedSnapshot(): Snapshot | undefined {
  const q = useQuery(snapshotQuery);
  return q.isError ? undefined : q.data;
}

// useSnapshot 取当前快照，界面跟着重读更新。只在快照读到之后渲染的组件里用(主界面三栏、抽屉、弹窗)。
export function useSnapshot(): Snapshot {
  const { data } = useQuery(snapshotQuery);
  if (!data) throw new Error("useSnapshot before the snapshot is loaded");
  return data;
}

// currentSnapshot 是写操作流程开始时界面上的快照;流程只在快照读到之后由界面发起。
export function currentSnapshot(): Snapshot {
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  if (!s) throw new Error("currentSnapshot before the snapshot is loaded");
  return s;
}

// ---------- 其他只读数据 ----------

// configQuery 是 GitHub 令牌与同步设置有没有配置;设置页保存后经 reloadLibrary 或 invalidateConfig 重读。
export const configQuery = queryOptions({
  queryKey: ["config"],
  queryFn: ({ signal }) => wails(signal, LibraryService.Config()),
});

export function invalidateConfig() {
  return queryClient.invalidateQueries({ queryKey: configQuery.queryKey });
}

// scopeDirectoryQuery 查作用域对应的 Agent 目录。home 是快照里的主目录，设置里改选 WSL 发行版后它变了，同一个作用域也要重查。
export function scopeDirectoryQuery(scope: ScopeKey | null, home: string | undefined) {
  return queryOptions({
    queryKey: ["scope-directory", scope, home],
    queryFn: ({ signal }) => wails(signal, LibraryService.ScopeDirectory(parseScope(scope!))),
    enabled: !!scope,
  });
}

// 版本按 ID 读，内容不会变：浏览途中技能被更新，读到的仍是同一版本。
export const versionQuery = (id: VersionID) =>
  queryOptions({ queryKey: ["version", id], queryFn: ({ signal }) => wails(signal, LibraryService.GetVersion(id)), meta: stable });

export const versionFilesQuery = (id: VersionID) =>
  queryOptions({ queryKey: ["version-files", id], queryFn: ({ signal }) => list(wails(signal, LibraryService.ListVersionFiles(id))), meta: stable });

export const versionFileQuery = (id: VersionID, path: string) =>
  queryOptions({
    queryKey: ["version-file", id, path],
    queryFn: ({ signal }) => wails(signal, LibraryService.GetVersionFile(id, path)),
    meta: stable,
  });

// versionsQuery 是一个技能的版本历史;更新、回滚、重新读取后有新版本，随写操作重读。
// 写操作后快照和它一起重读：先等快照读完，技能已被移除时不再去读它的版本历史(抽屉随后关掉)。
export const versionsQuery = (skillID: SkillID) =>
  queryOptions({
    queryKey: ["versions", skillID],
    queryFn: async ({ signal }) => {
      const s = await queryClient.fetchQuery(snapshotQuery);
      if (!s.skills.some((x) => x.id === skillID)) return [];
      return list(wails(signal, LibraryService.ListVersions(skillID)));
    },
  });

// skillAuditQuery 是 skills.sh 上这个技能的审计，写操作后不重读，用户点重试时才重查。
export const skillAuditQuery = (skillID: SkillID) =>
  queryOptions({ queryKey: ["skill-audit", skillID], queryFn: ({ signal }) => wails(signal, LibraryService.GetSkillAudit(skillID)), meta: stable });

// updateHistoryQuery 是更新历史;回滚、重试之后随写操作重读。
export const updateHistoryQuery = (filter: UpdateHistoryFilter) =>
  queryOptions({
    queryKey: ["update-history", filter],
    queryFn: ({ signal }) => list(wails(signal, LibraryService.ListUpdateHistory(filter))),
  });

// 这台机器上装的 WSL 发行版，与设置里选的发行版启动时没能打开的原因。
export const wslDistrosQuery = queryOptions({
  queryKey: ["wsl-distros"],
  queryFn: ({ signal }) => list(wails(signal, LibraryService.ListWSLDistros())),
});

export const wslDistroErrorQuery = queryOptions({
  queryKey: ["wsl-distro-error"],
  queryFn: ({ signal }) => wails(signal, AppService.WSLDistroError()),
});

// ---------- 在 skills.sh 上找到的来源 ----------

// 来源记录认不出来源的未纳管技能，列出之后在后台去 skills.sh 找来源，找到的陆续补到界面上。
// found 按作用域与文件夹名记下这次打开 app 以来找过的：值是找到的来源，找过没找到的为 ""，不再重复找;
// 找的过程中出错(取消、技能库报错)的不记，下次重读时再找。
let found: ReadonlyMap<string, string> = new Map();
const searching = new Set<string>();
const listeners = new Set<() => void>();

function folderKey(scope: Scope, name: string): string {
  return `${scopeKey(scope)}/${name}`;
}

function findSources(unmanaged: UnmanagedSkill[]) {
  const pending = unmanaged.filter((u) => {
    const key = folderKey(u.scope, u.name);
    return !u.source_input && !found.has(key) && !searching.has(key);
  });
  if (pending.length === 0) return;
  const keys = pending.map((u) => folderKey(u.scope, u.name));
  for (const key of keys) searching.add(key);
  LibraryService.FindUnmanagedSources({ folders: pending.map((u) => ({ scope: u.scope, name: u.name })) }).then(
    (results) => {
      const next = new Map(found);
      for (const key of keys) next.set(key, "");
      for (const r of results ?? []) next.set(folderKey(r.scope, r.name), r.source_input);
      for (const key of keys) searching.delete(key);
      found = next;
      for (const listener of listeners) listener();
    },
    () => {
      for (const key of keys) searching.delete(key);
    },
  );
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

// FoundSources 是在 skills.sh 上找到的来源，用 foundSource 取一个未纳管技能的。
export type FoundSources = ReadonlyMap<string, string>;

// useFoundSources 订阅在 skills.sh 上找到的来源：找到新的时重新渲染。
export function useFoundSources(): FoundSources {
  return useSyncExternalStore(subscribe, () => found);
}

// foundSource 是在 skills.sh 上给这个未纳管技能找到的来源;来源记录认得出、还没找完或没找到时为 undefined。
export function foundSource(sources: FoundSources, skill: UnmanagedSkill): string | undefined {
  if (skill.source_input) return undefined;
  return sources.get(folderKey(skill.scope, skill.name)) || undefined;
}

// withFoundSource 给来源记录认不出来源的那一条补上在 skills.sh 上找到的来源(取第一处找到的),
// 和来源记录认出的一样预填、分组。
export function withFoundSource(sources: FoundSources, planned: PlannedUnmanagedSkill): PlannedUnmanagedSkill {
  if (planned.source_input || planned.error) return planned;
  for (const folder of planned.folders ?? []) {
    const input = foundSource(sources, folder);
    if (input) return { ...planned, source_input: input, source_ref: undefined };
  }
  return planned;
}
