// 桌面端读取技能库的入口：经 Wails 绑定调用 Go 侧的 LibraryService。
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
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
  type UnmanagedSkill,
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

export type SnapshotState =
  | { status: "loading" }
  | { status: "ready"; snapshot: Snapshot }
  | { status: "error"; error: LibraryError };

// Go 的 nil 切片经 JSON 变成 null,这里统一成空数组。
function list<T>(call: CancellablePromise<T[] | null>): Promise<T[]> {
  return call.then((value) => value ?? []);
}

// useLibrarySnapshot 读取主界面需要的全部只读数据;前端在自己的写操作之后用 reload 重读。
// 组件卸载或重读时取消进行中的调用。
export function useLibrarySnapshot(): [SnapshotState, () => void] {
  const [state, setState] = useState<SnapshotState>({ status: "loading" });
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    const calls: CancellablePromise<unknown>[] = [];
    const track = <T,>(call: CancellablePromise<T>) => {
      calls.push(call);
      return call;
    };
    let cancelled = false;
    Promise.all([
      track(LibraryService.Info()),
      list(track(LibraryService.ListAgents())),
      list(track(LibraryService.ListRegisteredProjects())),
      list(track(LibraryService.ListSkills())),
      list(track(LibraryService.ListCollections())),
      list(track(LibraryService.ListGroups())),
      list(track(LibraryService.ListGroupInstalls())),
      list(track(LibraryService.InspectSkillScopes({}))),
      list(track(LibraryService.ListUnmanagedSkills())),
      list(track(LibraryService.ListSameNameSkills())),
      track(AppService.Settings()),
    ]).then(
      ([info, agents, projects, skills, collections, groups, groupInstalls, skillScopes, unmanaged, sameName, settings]) => {
        if (cancelled) return;
        const agentsShown = settings.agents_shown ?? {};
        findSources(unmanaged);
        setState({
          status: "ready",
          snapshot: { info, agents, agentsShown, projects, skills, collections, groups, groupInstalls, skillScopes, unmanaged, sameName },
        });
      },
      (err: unknown) => {
        if (cancelled) return;
        setState({ status: "error", error: toLibraryError(err) });
      },
    );
    return () => {
      cancelled = true;
      for (const call of calls) void call.cancel();
    };
  }, [generation]);

  // reload 保留当前数据直到新快照到达，写操作后界面不闪"加载中";出错时才换成错误状态。
  const reload = useCallback(() => setGeneration((n) => n + 1), []);

  return [state, reload];
}

// useScopeDirectory 查作用域对应的 Agent 目录;scope 为 null 时不请求。home 是快照里的主目录，
// 设置里改选 WSL 发行版后它变了，同一个作用域也要重查。
export function useScopeDirectory(scope: ScopeKey | null, home: string | undefined): string | undefined {
  const [directory, setDirectory] = useState<string>();
  useEffect(() => {
    setDirectory(undefined);
    if (!scope) return;
    const call = LibraryService.ScopeDirectory(parseScope(scope));
    call.then(setDirectory, () => setDirectory(undefined));
    return () => void call.cancel();
  }, [scope, home]);
  return directory;
}

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
