// 桌面端读取技能库的入口：经 Wails 绑定调用 Go 侧的 LibraryService,读到的数据放在 TanStack Query 的缓存里。
// 写操作完成后 reloadLibrary 让缓存失效，界面上正在用的查询随之重读(前端在自己的写操作之后重读，见 ADR 0008)。
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
  type Scope,
  type SkillID,
  type UnmanagedSkill,
  type MentionsFilter,
  type MissingMention,
  type UpdateHistoryFilter,
  type VersionID,
} from "@bindings/internal/library";
import { parseScope, scopeKey, type ScopeKey } from "./scope";

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

// Attempt 是逐项调用技能库(components/app/results.tsx 的 runEach)时一项的结果：做成的是 value,失败的是转好的错误。
// 结果按下标与各项一一对应，跳过的那一项是 undefined。
export type Attempt<T> = { value: T } | { error: LibraryError };

// Snapshot 是主界面一次读出的全部只读数据;agentsShown、autoTranslate、foldCollections、foldBelow 来自应用设置，其余来自技能库。
// 外观不在这里，由 lib/theme.ts 按设置写到根元素上。
export interface Snapshot {
  info: AppInfo;
  agents: Agent[];
  agentsShown: Partial<Record<string, boolean>>;
  autoTranslate: boolean;
  foldCollections: boolean;
  foldBelow: number;
  projects: RegisteredProject[];
  skills: Skill[];
  collections: Collection[];
  groups: Group[];
  groupInstalls: GroupInstall[];
  skillScopes: SkillScopeInspection[];
  unmanaged: UnmanagedSkill[];
  sameName: SameNameSkill[];
  // missingMentions 是装在各作用域的技能提到了、Agent 在那里却找不到的技能(作用域页的提醒、技能详情的「提到了」用它)。
  missingMentions: MissingMention[];
}

// ---------- 查询缓存 ----------

// 缓存里的数据只在 reloadLibrary 时重读：写操作之后、用户点重试时，以及窗口切回前台时(App.tsx 的 focus 监听)。
// TanStack 自己的 refetchOnWindowFocus 关着，失败也不自动重试，免得 Go 端报的错被悄悄重试掉；
// 切回窗口时，上次读失败、界面还在用的查询会随 reloadLibrary 再读一次。
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: Infinity, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false },
  },
});

// stable 标记写操作后不用重读的查询：内容只由 key 决定(某个版本的文件列表),或者来自技能库以外(skills.sh 的审计)。
// ids 标记 key 里带技能库 ID(技能、版本、集合、组、项目)的查询：拉回整库之后，新库里同一个 ID 可能是另一样东西，
// forgetLibraryCaches 清空它们重读。
declare module "@tanstack/react-query" {
  interface Register {
    queryMeta: { stable?: boolean; ids?: boolean };
  }
}
const ids = { ids: true };
const stableIDs = { stable: true, ids: true };

// reloadLibrary 让查询缓存失效，界面上正在用的查询立即重读，其余的下次用到时再读;重读期间保留旧数据，界面不闪"加载中"。
export function reloadLibrary(): Promise<void> {
  return queryClient.invalidateQueries({ predicate: (q: Query) => !q.meta?.stable });
}

// forgetLibraryCaches 在拉回整库之后丢掉按旧库缓存的内容，等快照和界面上正在用的查询都按新库重读完：
// key 里带技能库 ID 的查询(包括写操作后不重读的版本、文件列表与审计)清空重读，新库里同一个 ID 可能是另一个技能或版本;
// 其余查询照常重读。由 view.ts 的 forgetLibrary 调用，界面上按 ID 记下的状态与在 skills.sh 上找到的来源在那里一起作废。
export async function forgetLibraryCaches(): Promise<void> {
  await Promise.all([queryClient.resetQueries({ predicate: (q: Query) => !!q.meta?.ids }), reloadLibrary()]);
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
  // 列出未纳管技能时，技能库先把和库里某一版一致的文件夹记为安装：先读它，安装记录、巡检与同名才把这几处算进去。
  const unmanaged = await list(wails(signal, LibraryService.ListUnmanagedSkills()));
  const [info, agents, projects, skills, collections, groups, groupInstalls, skillScopes, sameName, missingMentions, settings] = await Promise.all([
    wails(signal, LibraryService.Info()),
    list(wails(signal, LibraryService.ListAgents())),
    list(wails(signal, LibraryService.ListRegisteredProjects())),
    list(wails(signal, LibraryService.ListSkills())),
    list(wails(signal, LibraryService.ListCollections())),
    list(wails(signal, LibraryService.ListGroups())),
    list(wails(signal, LibraryService.ListGroupInstalls())),
    list(wails(signal, LibraryService.InspectSkillScopes({}))),
    list(wails(signal, LibraryService.ListSameNameSkills())),
    // 提到关系只是提示：读不出来时按没有，不让整个主界面打不开。
    list(wails(signal, LibraryService.ListMissingMentions())).catch(() => []),
    wails(signal, AppService.Settings()),
  ]);
  const agentsShown = settings.agents_shown ?? {};
  return {
    info,
    agents,
    agentsShown,
    autoTranslate: settings.auto_translate,
    foldCollections: settings.fold_collections,
    foldBelow: settings.fold_below,
    projects,
    skills,
    collections,
    groups,
    groupInstalls,
    skillScopes,
    unmanaged,
    sameName,
    missingMentions,
  };
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

// gitHubTokenQuery 是设置页填的 GitHub 令牌;它存在数据库里，拉回后经 reloadLibrary 重读。
export const gitHubTokenQuery = queryOptions({
  queryKey: ["github-token"],
  queryFn: ({ signal }) => wails(signal, LibraryService.GitHubToken()),
});

export function invalidateGitHubToken() {
  return queryClient.invalidateQueries({ queryKey: gitHubTokenQuery.queryKey });
}

// aiSettingsQuery 是设置页填的 AI 设置;它存在数据库里，拉回后经 reloadLibrary 重读。
export const aiSettingsQuery = queryOptions({
  queryKey: ["ai-settings"],
  queryFn: ({ signal }) => wails(signal, LibraryService.AISettings()),
});

export function invalidateAISettings() {
  return queryClient.invalidateQueries({ queryKey: aiSettingsQuery.queryKey });
}

// syncSettingsQuery 是设置页填的同步设置;它存在数据库里、只属于这台机器，拉回时不变。
export const syncSettingsQuery = queryOptions({
  queryKey: ["sync-settings"],
  queryFn: ({ signal }) => wails(signal, LibraryService.SyncSettings()),
});

export function invalidateSyncSettings() {
  return queryClient.invalidateQueries({ queryKey: syncSettingsQuery.queryKey });
}

// syncStatusQuery 是这台机器在 S3 上的设备目录与上次推送的时间，加上 Go 端记下的上一次推送(自动或手动)出的问题。
// Go 端自动推送后发 sync-pushed 事件，App 据此重读;手动推送、改同步设置后由发起处重读。
export const syncStatusQuery = queryOptions({
  queryKey: ["sync-status"],
  queryFn: async ({ signal }) => {
    const [status, problem] = await Promise.all([wails(signal, LibraryService.SyncStatus()), wails(signal, AppService.PushProblem())]);
    return { ...status, problem };
  },
});

export function invalidateSyncStatus() {
  return queryClient.invalidateQueries({ queryKey: syncStatusQuery.queryKey });
}

// scopeDirectoryQuery 查作用域对应的 Agent 目录。home 是快照里的主目录，设置里改选 WSL 发行版后它变了，同一个作用域也要重查。
export function scopeDirectoryQuery(scope: ScopeKey | null, home: string | undefined) {
  return queryOptions({
    queryKey: ["scope-directory", scope, home],
    queryFn: ({ signal }) => wails(signal, LibraryService.ScopeDirectory(parseScope(scope!))),
    enabled: !!scope,
    meta: ids,
  });
}

// sameNameCopiesQuery 比较几个作用域里不是库装的技能文件夹的内容(整批入库的计划，见 PlanFetchUnmanaged),
// 用来认出同名但内容不同的几份;作用域页只在别处有同名的未纳管技能时才查(见 lib/same-name 的 sameNameScopes)。只读，随写操作重读。
export function sameNameCopiesQuery(scopes: Scope[]) {
  return queryOptions({
    queryKey: ["same-name-copies", scopes.map(scopeKey).sort()],
    queryFn: ({ signal }) => list(wails(signal, LibraryService.PlanFetchUnmanaged({ scopes }))),
    enabled: scopes.length > 1,
    meta: ids,
  });
}

// 版本按 ID 读，在同一个库里内容不会变：浏览途中技能被更新，读到的仍是同一版本;拉回整库之后重读(forgetLibraryIDs)。
export const versionQuery = (id: VersionID) =>
  queryOptions({ queryKey: ["version", id], queryFn: ({ signal }) => wails(signal, LibraryService.GetVersion(id)), meta: stableIDs });

export const versionFilesQuery = (id: VersionID) =>
  queryOptions({ queryKey: ["version-files", id], queryFn: ({ signal }) => list(wails(signal, LibraryService.ListVersionFiles(id))), meta: stableIDs });

// versionFileQuery 读某个版本里的一个文件：内容不会变，但带着的译文会变(翻译、拉回),所以随写操作重读。
export const versionFileQuery = (id: VersionID, path: string) =>
  queryOptions({
    queryKey: ["version-file", id, path],
    queryFn: ({ signal }) => wails(signal, LibraryService.GetVersionFile(id, path)),
    meta: ids,
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
    meta: ids,
  });

// skillAuditQuery 是 skills.sh 上这个技能的审计，写操作后不重读，用户点重试或拉回整库之后才重查。
export const skillAuditQuery = (skillID: SkillID) =>
  queryOptions({ queryKey: ["skill-audit", skillID], queryFn: ({ signal }) => wails(signal, LibraryService.GetSkillAudit(skillID)), meta: stableIDs });

// updateHistoryQuery 是更新历史;回滚、重试之后随写操作重读。
export const updateHistoryQuery = (filter: UpdateHistoryFilter) =>
  queryOptions({
    queryKey: ["update-history", filter],
    queryFn: ({ signal }) => list(wails(signal, LibraryService.ListUpdateHistory(filter))),
    meta: ids,
  });

// mentionsQuery 是关系图里集合或组的技能提到了哪些技能，以及还有几处单个词的技能名没交给 AI 判断;
// 技能入库、更新、移除之后随写操作重读，AI 判断完也重读。
export const mentionsQuery = (filter: MentionsFilter) =>
  queryOptions({
    queryKey: ["mentions", filter],
    queryFn: ({ signal }) =>
      wails(signal, LibraryService.ListMentions(filter)).then((result) => ({ mentions: result.mentions ?? [], unjudged: result.unjudged })),
    meta: ids,
  });

// skillMentionsQuery 是技能详情里这个技能提到了谁、被谁提到，以及还有几处单个词的技能名没交给 AI 判断;
// 随写操作重读，AI 判断完也重读。
export const skillMentionsQuery = (id: SkillID) =>
  queryOptions({
    queryKey: ["skill-mentions", id],
    queryFn: ({ signal }) =>
      wails(signal, LibraryService.GetSkillMentions(id)).then((result) => ({
        mentions: result.mentions ?? [],
        mentionedBy: result.mentioned_by ?? [],
        unjudged: result.unjudged,
      })),
    meta: ids,
  });

// 这台机器上装的 WSL 发行版，与设置里选的发行版启动时没能打开的原因。
export const wslDistrosQuery = queryOptions({
  queryKey: ["wsl-distros"],
  queryFn: ({ signal }) => list(wails(signal, LibraryService.ListWSLDistros())),
});

// useHasWSL 是这台机器装没装 WSL 发行版(只有 Windows 上会有):路径输入据此决定 / 开头的算不算发行版里的路径、要不要写出会被理解成哪里。
export function useHasWSL(): boolean {
  return (useQuery(wslDistrosQuery).data ?? []).length > 0;
}

export const wslDistroErrorQuery = queryOptions({
  queryKey: ["wsl-distro-error"],
  queryFn: ({ signal }) => wails(signal, AppService.WSLDistroError()),
});

// 本机的网络代理设置与当前探测到的系统静态代理;窗口重新获得焦点时会随 reloadLibrary 重读系统代理。
export const proxySettingsQuery = queryOptions({
  queryKey: ["proxy-settings"],
  queryFn: ({ signal }) => wails(signal, LibraryService.ProxySettings()),
});

export function invalidateProxySettings() {
  return queryClient.invalidateQueries({ queryKey: proxySettingsQuery.queryKey });
}

export const systemProxyQuery = queryOptions({
  queryKey: ["system-proxy"],
  queryFn: ({ signal }) => wails(signal, LibraryService.SystemProxy()),
  refetchOnMount: "always",
});

export function invalidateSystemProxy() {
  return queryClient.invalidateQueries({ queryKey: systemProxyQuery.queryKey });
}

// releaseQuery 是检查 Mechbox 新版本的现状：Go 端启动时与之后每天检查一次，检查完发 release-checked 事件，App 据此重读;
// 设置页「关于」里手动检查之后由发起处重读。
export const releaseQuery = queryOptions({
  queryKey: ["release"],
  queryFn: ({ signal }) => wails(signal, AppService.Release()),
});

export function invalidateRelease() {
  return queryClient.invalidateQueries({ queryKey: releaseQuery.queryKey });
}
