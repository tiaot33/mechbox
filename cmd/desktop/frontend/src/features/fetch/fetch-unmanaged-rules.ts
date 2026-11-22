// 一次确认入库未纳管技能(fetch-unmanaged-all.tsx)里的判断：一条最终用的来源与入库请求，确认框里按来源怎么分组、
// 同名不同内容的单列，勾上的里哪些要另外提醒，确认后按来源怎么分批。默认勾哪些见 lib/consent.ts 的 fetchPlan。
// 不碰界面、不调技能库，单元测试见 fetch-unmanaged-rules.test.ts。
import type { PlannedUnmanagedSkill, UnmanagedFetchItem } from "@bindings/internal/library";
import { repoOwned } from "@/lib/unmanaged";

export const noSource = "";

// identicalGroup 是和库里某个技能一样的那一组的键(不会和来源的键撞上):入库只补记安装，不带来源。
export const identicalGroup = "\u0000library";

// sourceKey 把来源与分支合成一个键：同一个来源只读一次，结果也按它分组。
export function sourceKey(input?: string, ref?: string): string {
  return input ? `${input}\n${ref ?? ""}` : noSource;
}

export function foldersOf(planned: PlannedUnmanagedSkill) {
  return planned.folders ?? [];
}

// outsideTarget 是指向 Agent 目录以外的那一处链接指向的地方。
export function outsideTarget(planned: PlannedUnmanagedSkill) {
  return foldersOf(planned).find((f) => f.link_outside)?.link_target ?? "";
}

// keyOf 是计划里一条的稳定身份：第一处文件夹。
export function keyOf(planned: PlannedUnmanagedSkill): string {
  return foldersOf(planned)[0]?.folder ?? planned.name;
}

// sourceOf 是一条最终用的来源：用户改过链接就用改过的(留空就是没有来源);来源记录里的分支只跟着它认出的那个来源走。
export function sourceOf(
  planned: PlannedUnmanagedSkill,
  link?: string,
): { input?: string; ref?: string } {
  if (link === undefined)
    return { input: planned.source_input, ref: planned.source_ref };
  const input = link.trim();
  return {
    input: input || undefined,
    ref:
      input && input === planned.source_input ? planned.source_ref : undefined,
  };
}

export function itemOf(
  planned: PlannedUnmanagedSkill,
  link?: string,
): UnmanagedFetchItem {
  const { input, ref } = sourceOf(planned, link);
  return {
    folders: foldersOf(planned).map((f) => ({ scope: f.scope, directory: f.directory, name: f.name })),
    source_input: input,
    source_ref: ref,
  };
}

// fetchGroups 是确认框里计划的各条(按下标)怎么列:usable 是能入库的，unusable 是不能入库的(写出原因);
// conflicts 是同名不同内容的几份，按名字单列、只选一份;其余按来源分组(和库里某个技能一样的单独一组，入库只记下装在哪里):
// ordered 是各组，有来源的在前、已经在技能库里的其次、没有来源的在最后，同一类按键排。
export function fetchGroups(plan: PlannedUnmanagedSkill[]) {
  const usable = plan.flatMap((p, i) => (p.error ? [] : [i]));
  const conflicts = new Map<string, number[]>();
  const groups = new Map<string, number[]>();
  for (const i of usable) {
    const p = plan[i];
    const key = p.skill_id ? identicalGroup : sourceKey(p.source_input, p.source_ref);
    if (p.conflict) conflicts.set(p.name, [...(conflicts.get(p.name) ?? []), i]);
    else groups.set(key, [...(groups.get(key) ?? []), i]);
  }
  const rank = (key: string) => (key === noSource ? 2 : key === identicalGroup ? 1 : 0);
  return {
    usable,
    unusable: plan.filter((p) => p.error),
    conflicts,
    ordered: [...groups].sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b)),
  };
}

// pickedNotes 是勾上的各条(按下标的先后)里要另外提醒的:team 是归项目仓库管的(提交在仓库里、记在项目 skills-lock.json 里),
// outside 是指向 Agent 目录以外的。
export function pickedNotes(plan: PlannedUnmanagedSkill[], picked: ReadonlySet<number>) {
  const chosen = [...picked].sort((a, b) => a - b);
  return { chosen, team: chosen.map((i) => plan[i]).filter(repoOwned), outside: chosen.map((i) => plan[i]).filter((p) => p.link_outside) };
}

// fetchBatches 把要入库的几条按最终用的来源分批(同一个来源只读一次),键是 sourceKey;没有来源的一批在最后，其余照出现的先后。
export function fetchBatches<T extends { planned: PlannedUnmanagedSkill; link?: string }>(items: T[]): [string, T[]][] {
  const batches = new Map<string, T[]>();
  for (const item of items) {
    const { input, ref } = sourceOf(item.planned, item.link);
    const key = sourceKey(input, ref);
    batches.set(key, [...(batches.get(key) ?? []), item]);
  }
  return [...batches].sort(([a], [b]) => (a === noSource ? 1 : b === noSource ? -1 : 0));
}
