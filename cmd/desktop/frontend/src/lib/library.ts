// 桌面端读取技能库的入口：经 Wails 绑定调用 Go 侧的 LibraryService。
import { useCallback, useEffect, useState } from "react";
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
  type UnmanagedSkill,
} from "@bindings/internal/library";
import { parseScope, type ScopeKey } from "./model";

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

// useScopeDirectory 查作用域对应的 Agent 目录;scope 为 null 时不请求。
export function useScopeDirectory(scope: ScopeKey | null): string | undefined {
  const [directory, setDirectory] = useState<string>();
  useEffect(() => {
    setDirectory(undefined);
    if (!scope) return;
    const call = LibraryService.ScopeDirectory(parseScope(scope));
    call.then(setDirectory, () => setDirectory(undefined));
    return () => void call.cancel();
  }, [scope]);
  return directory;
}
