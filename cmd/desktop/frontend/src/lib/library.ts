// 桌面端读取技能库的入口：经 Wails 绑定调用 Go 侧的 LibraryService。
import { useCallback, useEffect, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService, type AppInfo } from "@bindings/cmd/desktop";
import type {
  Agent,
  Collection,
  GroupActivation,
  GroupWithMembers,
  ProjectionInspection,
  RegisteredProject,
  SkillSummary,
} from "@bindings/internal/library";

// LibraryError 与 Go 侧 library.Error 的 JSON 形状一致;Go 端的 MarshalError
// 把任意错误规范成它，Wails 运行时放在被拒绝的 Error 的 cause 上。
export interface LibraryError {
  code: string;
  message: string;
  details?: Record<string, unknown>;
}

export function toLibraryError(err: unknown): LibraryError {
  const cause = err instanceof Error ? err.cause : undefined;
  if (cause && typeof cause === "object" && "code" in cause && "message" in cause) {
    return cause as LibraryError;
  }
  return { code: "internal", message: err instanceof Error ? err.message : String(err) };
}

// Snapshot 是主界面一次读出的全部只读数据。
export interface Snapshot {
  info: AppInfo;
  agents: Agent[];
  projects: RegisteredProject[];
  skills: SkillSummary[];
  collections: Collection[];
  groups: GroupWithMembers[];
  groupActivations: GroupActivation[];
  projections: ProjectionInspection[];
}

export type SnapshotState =
  | { status: "loading" }
  | { status: "ready"; snapshot: Snapshot }
  | { status: "error"; error: LibraryError };

// Go 的 nil 切片经 JSON 变成 null,这里统一成空数组。
function list<T>(call: CancellablePromise<T[] | null>): Promise<T[]> {
  return call.then((value) => value ?? []);
}

// useLibrarySnapshot 读取主界面需要的全部只读数据;reload 用于重试，
// 以后前端在自己的写操作之后也用它重读。组件卸载或重读时取消进行中的调用。
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
      list(track(LibraryService.ListGroupsWithMembers())),
      list(track(LibraryService.ListGroupActivations())),
      list(track(LibraryService.InspectProjections({}))),
    ]).then(
      ([info, agents, projects, skills, collections, groups, groupActivations, projections]) => {
        if (cancelled) return;
        setState({
          status: "ready",
          snapshot: { info, agents, projects, skills, collections, groups, groupActivations, projections },
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

  const reload = useCallback(() => {
    setState({ status: "loading" });
    setGeneration((n) => n + 1);
  }, []);

  return [state, reload];
}

// useScopeDirectory 按 Agent 现行布局解析作用域目录;scope 为 null 时不请求。
export function useScopeDirectory(scope: Parameters<typeof LibraryService.Resolve>[0] | null): string | undefined {
  const [directory, setDirectory] = useState<string>();
  const key = scope ? `${scope.agent}@${scope.project_id ?? 0}` : "";
  useEffect(() => {
    setDirectory(undefined);
    if (!scope) return;
    const call = LibraryService.Resolve(scope);
    call.then(setDirectory, () => setDirectory(undefined));
    return () => void call.cancel();
    // scope 对象每次渲染都是新的，按身份 key 触发。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  return directory;
}
