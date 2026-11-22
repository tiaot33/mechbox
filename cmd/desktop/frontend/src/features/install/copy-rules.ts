// 照着装(copy.tsx)里的判断：照着另一个 Agent、另一个项目装时装哪些，确认框里各项的勾选键、一处标题上的勾选、
// 要另外说出的改过的与新版的技能，确认后按勾选逐处要做的事和结果怎么归并。默认勾哪些、覆盖哪些见 lib/consent.ts。
// 不碰界面、不调技能库，单元测试见 copy-rules.test.ts。
import {
  GroupMemberOutcome,
  SkillScopeStatus,
  type Agent,
  type AgentCode,
  type Group,
  type GroupMemberResult,
  type ProjectID,
  type RegisteredProject,
  type Scope,
  type Skill,
  type SkillID,
  type SkillInScope,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import { copyable, copyDefaults, copyOverwrites, type CopyPlanned } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { Attempt, LibraryError, Snapshot } from "@/lib/library";
import { errorOccupier, skillScopesIn } from "@/lib/installs";
import { sortSkills } from "@/lib/pages";
import { lastSegment } from "@/lib/paths";
import { mergeProjectAgents, mergedScope, placeCount, sameFolderAt, scopeLabel, type AgentList } from "@/lib/places";
import { placeOf, scopeKey, scopeOf, type Place, type ScopeKey } from "@/lib/scope";
import { notTakenOver, repoOwned, unmanagedIn } from "@/lib/unmanaged";
import { memberRows, type ResultRow } from "@/components/app/result-rows";
import { occupied } from "./install-rules";

// ---------- 装哪些 ----------
// 照着另一个 Agent 装：把它在全局和各已登记项目里装的库里技能装到这个 Agent 的同一处。
// 照着另一个项目装：把各 Agent 在那个项目里装的库里技能装到这个项目里的同一个 Agent。
// 两种都是一处照着一处装：直接安装照样直接安装，组安装照样是同一个组的组安装。只装一次、只加不减:
// 装到的那一处已经装了的技能、已经有的组安装不列出;两处读同一个目录时本来就一样，也不列出。

// CopyPlace 是照装时装到的一处(to)。groups 是照着的那一处有、这里还没有的组安装，skills 是那一处直接安装、这里还没装的技能;
// edited 是要装过来的技能里(含随组装过来的),那一处被手动改过的：装的是库里的版本，不是改过的那份。
// newer 是那一处在 app 外更新到了作者新版的：装的同样是库里的版本，先更新才能用上作者的新版。
// missing 是 to 所在的项目不在这台机器上，这一处装不了。
export interface CopyPlace {
  to: Scope;
  groups: Group[];
  skills: Skill[];
  edited: Skill[];
  newer: Skill[];
  missing: boolean;
}

// copyTarget 是照装到 to 时这一处装在谁的作用域上：项目里共用一个目录的几个 Agent 合成一处，落在代表的 Agent 上，
// 和右栏、作用域页一致(组安装记在代表上，合成的那一行才显示为已组安装)。
function copyTarget(s: AgentList, to: AgentCode, place: Place): AgentCode {
  return mergedScope(s, scopeOf(to, place)).agent;
}

// hasGroupAt 是某个组在一处是否已组安装给这个 Agent:组安装记在和它读同一个目录的另一个 Agent 上也算。
function hasGroupAt(s: Snapshot, g: Group, agent: AgentCode, place: Place): boolean {
  return s.groupInstalls.some((gi) => gi.group_id === g.id && placeOf(gi.scope) === place && sameFolderAt(s, gi.scope.agent, agent, place));
}

// copyPlace 是照着 from 装到 to 的这一处;没有要装的时为空。
function copyPlace(s: Snapshot, from: Scope, to: Scope): CopyPlace[] {
  const installed = new Set(skillScopesIn(s, scopeKey(to)).map((p) => p.skill_id));
  const groups = s.groups.filter((g) => hasGroupAt(s, g, from.agent, placeOf(from)) && !hasGroupAt(s, g, to.agent, placeOf(to)));
  const there = skillScopesIn(s, scopeKey(from)).filter((p) => !installed.has(p.skill_id));
  const skillsOf = (ids: SkillID[]) => sortSkills(s, s.skills.filter((x) => ids.includes(x.id)));
  const skills = skillsOf(there.filter((p) => p.direct).map((p) => p.skill_id));
  if (!groups.length && !skills.length) return [];
  const coming = new Set([...skills.map((x) => x.id), ...groups.flatMap((g) => g.members ?? [])]);
  const modified = there.filter((p) => p.status === SkillScopeStatus.SkillScopeModified && coming.has(p.skill_id));
  const edited = skillsOf(modified.filter((p) => !p.source_latest).map((p) => p.skill_id));
  const newer = skillsOf(modified.filter((p) => p.source_latest).map((p) => p.skill_id));
  const missing = !!s.projects.find((p) => p.id === placeOf(to))?.missing;
  return [{ to, groups, skills, edited, newer, missing }];
}

export function copyFromAgent(s: Snapshot, from: AgentCode, to: AgentCode): CopyPlace[] {
  return [0, ...s.projects.map((p) => p.id)].flatMap((place) =>
    sameFolderAt(s, from, to, place) ? [] : copyPlace(s, scopeOf(from, place), scopeOf(copyTarget(s, to, place), place)),
  );
}

// copySources 是可以照着装的 Agent:to 以外、照装时有东西可装的(只在不在这台机器上的项目里有的不算),排在最前的是默认照着的。
// at 是从哪个作用域页打开的：从全局的(at 为 0)打开时装得最多的在前;从项目的打开时，在这个项目里可以照着装的项数
// (copyItems,和确认框里这一处标题上的数一样)最多的在前，一样多的(包括谁都没有)再按装得多少排。再一样多的按 Agent 列表顺序。
export function copySources(s: Snapshot, to: AgentCode, at: Place): Agent[] {
  const total = (code: AgentCode) => s.skillScopes.filter((p) => p.scope.agent === code).length;
  return s.agents
    .flatMap((a) => {
      if (a.code === to) return [];
      const copy = copyFromAgent(s, a.code, to);
      if (!copy.some((p) => !p.missing)) return [];
      const here = copy.find((p) => at && placeOf(p.to) === at && !p.missing);
      return [{ a, here: here ? copyItems(here) : 0, total: total(a.code) }];
    })
    .sort((x, y) => y.here - x.here || y.total - x.total)
    .map((x) => x.a);
}

// copyItems 是照装时一处要装的项数：每个组安装、每个直接安装的技能各算一项。
export const copyItems = (p: CopyPlace) => p.groups.length + p.skills.length;

// UncopiedUnmanaged 是照装带不过去的未纳管技能(见 splitUncopied)。
export interface UncopiedUnmanaged {
  unmanaged: UnmanagedSkill[];
  repo: UnmanagedSkill[];
}

// splitUncopied 把照装带不过去的未纳管技能分开:unmanaged 是还没交给 app 管的(notTakenOver),
// 入库以后才能照着装;repo 是归项目仓库管的，随项目仓库走;指向 Agent 目录以外的两边都不算。
function splitUncopied(left: UnmanagedSkill[]): UncopiedUnmanaged {
  return { unmanaged: left.filter(notTakenOver), repo: left.filter((u) => repoOwned(u) && !u.link_outside) };
}

// uncopiedUnmanaged 是 from 里照装带不过去的未纳管技能(按 splitUncopied 分开)。和 to 读同一个目录的那几处不算;
// to 在同一处已经有同名文件夹的(如 npx skills 从这里链到 to 的那一份)也不算。
export function uncopiedUnmanaged(s: Snapshot, from: AgentCode, to: AgentCode): UncopiedUnmanaged {
  return splitUncopied(
    s.unmanaged.filter((u) => {
      const place = placeOf(u.scope);
      if (u.scope.agent !== from || sameFolderAt(s, from, to, place)) return false;
      const toKey = scopeKey(scopeOf(copyTarget(s, to, place), place));
      return !folderNamesIn(s, toKey).includes(u.name);
    }),
  );
}

// folderNamesIn 是作用域的技能目录里已有的技能文件夹名：未纳管的和库装的。
function folderNamesIn(s: Snapshot, key: ScopeKey): string[] {
  return [...unmanagedIn(s, key).map((x) => x.name), ...skillScopesIn(s, key).map((p) => lastSegment(p.folder))];
}

// projectAgentsUsing 是在项目里装了库里技能(直接安装或组安装)的 Agent,共用目录的合成一处(代表的 Agent)。
function projectAgentsUsing(s: Snapshot, project: ProjectID): Agent[] {
  const inProject = (scope: Scope, a: Agent) => scope.agent === a.code && placeOf(scope) === project;
  return mergeProjectAgents(
    s,
    s.agents.filter((a) => s.skillScopes.some((p) => inProject(p.scope, a)) || s.groupInstalls.some((g) => inProject(g.scope, a))),
  );
}

// copyFromProject 是照着项目 from 装到项目 to:各 Agent(共用目录的合成一处)在 from 装的，装到 to 里的同一个 Agent。
export function copyFromProject(s: Snapshot, from: ProjectID, to: ProjectID): CopyPlace[] {
  if (from === to) return [];
  return projectAgentsUsing(s, from).flatMap((a) => copyPlace(s, scopeOf(a.code, from), scopeOf(a.code, to)));
}

// projectInstalls 是项目里装的库里技能数，共用目录的几个 Agent 的同一个技能只算一次(和右栏项目行的数一致)。
function projectInstalls(s: Snapshot, project: ProjectID): number {
  return placeCount(s, s.skillScopes.filter((p) => placeOf(p.scope) === project));
}

// copyProjectSources 是可以照着装到 to 的其他已登记项目：装了库里技能、有东西可装的(只有空的组安装的不算),
// 装得最多的在前(默认选它),一样多的按项目列表顺序。
export function copyProjectSources(s: Snapshot, to: ProjectID): RegisteredProject[] {
  return s.projects
    .filter((p) => projectInstalls(s, p.id) > 0 && copyFromProject(s, p.id, to).length > 0)
    .sort((a, b) => projectInstalls(s, b.id) - projectInstalls(s, a.id));
}

// uncopiedProjectUnmanaged 是项目 from 里照装带不过去的未纳管技能(按 splitUncopied 分开;to 里同一个 Agent 已有同名文件夹的不算)。
export function uncopiedProjectUnmanaged(s: Snapshot, from: ProjectID, to: ProjectID): UncopiedUnmanaged {
  return splitUncopied(
    mergeProjectAgents(s, s.agents).flatMap((a) => {
      const there = folderNamesIn(s, scopeKey(scopeOf(a.code, to)));
      return unmanagedIn(s, scopeKey(scopeOf(a.code, from))).filter((u) => !there.includes(u.name));
    }),
  );
}

// ---------- 确认框与结果 ----------

// 勾选的 key:一处按装到的作用域区分，再加技能或组。
export const skillKey = (p: CopyPlace, id: number) => `s:${scopeKey(p.to)}:${id}`;
export const groupKey = (p: CopyPlace, id: number) => `g:${scopeKey(p.to)}:${id}`;
// checkedCounts 是勾上的项里直接安装的技能数和组安装数(按键的前缀分),写在确认按钮上。
export function checkedCounts(checked: ReadonlySet<string>): { skills: number; groups: number } {
  const skills = [...checked].filter((k) => k.startsWith("s:")).length;
  return { skills, groups: checked.size - skills };
}
export const placeKeys = (p: CopyPlace) => [...p.groups.map((g) => groupKey(p, g.id)), ...p.skills.map((x) => skillKey(p, x.id))];

// Planned 是一项的安装计划(见 consent.ts 的 CopyPlanned),读不出时的错误是技能库错误。Plans 按 skillKey / groupKey 存。
export type Planned = Exclude<CopyPlanned, { error: unknown }> | { error: LibraryError };
export type Plans = Map<string, Planned>;

// canCheck 是这一项能不能勾：组安装读出计划就能勾，直接安装还要装得了(没被占用)。
export const canCheck = (planned: Planned | undefined) =>
  !!planned && ("members" in planned || ("plan" in planned && copyable(planned.plan.status)));

// comingNotes 是确认框里另外说的、勾上的项里会装过来的技能：直接安装勾上了，或随勾上的组安装装过来。
// edited 是原来那里被改过的(装过来的是库里的版本),newer 是原来那里在 app 外更新到了作者新版的，都按名字去重。
export function comingNotes(places: CopyPlace[], checked: ReadonlySet<string>) {
  const coming = (p: CopyPlace, id: number) =>
    checked.has(skillKey(p, id)) || p.groups.some((g) => checked.has(groupKey(p, g.id)) && (g.members ?? []).includes(id));
  const names = (pick: (p: CopyPlace) => Skill[]) => [...new Set(places.flatMap((p) => pick(p).filter((x) => coming(p, x.id)).map((x) => x.name)))];
  return { edited: names((p) => p.edited), newer: names((p) => p.newer) };
}

// togglePlace 是一处标题上的勾选(keys 是这一处的各项):有勾上的就全部取消;一个都没勾时按 copyDefaults 整处勾上
// (不管这一处打开时默认勾不勾，不替用户勾上要覆盖的),读不出计划的项不勾。
export function togglePlace(checked: ReadonlySet<string>, keys: string[], plans: Plans | null): Set<string> {
  const next = new Set(checked);
  const any = keys.some((k) => next.has(k));
  const entries = keys.flatMap((k): [string, Planned][] => {
    const v = plans?.get(k);
    return v ? [[k, v]] : [];
  });
  const defaults = copyDefaults(entries).checked;
  for (const k of keys) {
    if (any) next.delete(k);
    else if (defaults.has(k)) next.add(k);
  }
  return next;
}

// meantPlaces 是确认后结果里要算的那几处：默认勾的，和用户勾了东西的;其余各处装不了、读不出计划的不算没装上。
export function meantPlaces(places: CopyPlace[], byDefault: (p: CopyPlace) => boolean, checked: ReadonlySet<string>): CopyPlace[] {
  return places.filter((p) => byDefault(p) || placeKeys(p).some((k) => checked.has(k)));
}

// CopyStep 是确认后的一步:skip 是不装、直接写进结果的一行(项目不在这台机器上、读不出计划、被占用);
// group 是一次组安装(overwrite 是勾了"覆盖",untracked 是其中有不是库装的同名文件夹的成员),skill 是一次直接安装。label 是结果里的写法。
export type CopyStep =
  | { skip: ResultRow }
  | { group: Group; scope: Scope; label: string; overwrite: boolean; untracked: Set<SkillID> }
  | { skill: Skill; scope: Scope; label: string; overwrite: boolean };

// copySteps 按勾选逐处要做的事：每一处先组安装，再直接安装;没勾的项不装，读不出计划、被占用的照样写进结果。
export function copySteps(s: Snapshot, places: CopyPlace[], plans: Plans, checked: ReadonlySet<string>, overwrite: ReadonlySet<string>): CopyStep[] {
  const t = messages().copy;
  const a = messages().actions;
  const skip = (label: string, reason: string, occupier?: SkillID): CopyStep => ({ skip: { label, outcome: a.memberSkipped, tone: "warn", reason, occupier } });
  return places.flatMap((p): CopyStep[] => {
    const scope = p.to;
    const where = scopeLabel(s, scope);
    if (p.missing) return [skip(where, t.missingPlace)];
    const groups = p.groups.flatMap((g): CopyStep[] => {
      const key = groupKey(p, g.id);
      const planned = plans.get(key);
      const label = t.groupFailed(g.name, where);
      if (planned && "error" in planned) return [skip(label, describeError(planned.error))];
      if (!checked.has(key)) return [];
      const untracked = new Set(planned && "members" in planned ? planned.members.filter((x) => copyOverwrites(x.status)).map((x) => x.skill_id) : []);
      return [{ group: g, scope, label, overwrite: overwrite.has(key), untracked }];
    });
    const skills = p.skills.flatMap((x): CopyStep[] => {
      const key = skillKey(p, x.id);
      const planned = plans.get(key);
      const label = `${x.name} → ${where}`;
      if (!planned) return [];
      if ("error" in planned) return [skip(label, describeError(planned.error))];
      if (!("plan" in planned)) return [];
      const { plan } = planned;
      if (!copyable(plan.status)) {
        const { reason, occupier } = occupied(s, x.id, plan);
        return [skip(label, reason, occupier)];
      }
      if (!checked.has(key)) return [];
      return [{ skill: x, scope, label, overwrite: copyOverwrites(plan.status) }];
    });
    return [...groups, ...skills];
  });
}

// copyResults 归并照着装的结果:outcomes 与 steps 一一对应(runEach 的结果):组安装做成的是逐个成员的结果，直接安装做成的是空列表;
// 跳过的和没做到的是 undefined。
// installed 按"哪一处的哪个技能"算，同一个技能既随组又直接安装时只算一次;groups 是记下的组安装数;placed 是装上的各处。
// 组安装里有不是库装的同名文件夹而没勾覆盖、被跳过的成员是用户自己选的跳过，不再列出;其余被跳过的成员照样列出原因。
// 直接安装执行时才被同一批里先装的同名技能占上的，错误里有占着的那个，照样能点开。failed 是有失败的(顶栏画叉)。
export function copyResults(s: Snapshot, steps: CopyStep[], outcomes: readonly (Attempt<GroupMemberResult[]> | undefined)[]) {
  const t = messages().actions;
  const rows: ResultRow[] = [];
  const installed = new Set<string>();
  const placed: SkillInScope[] = [];
  let groups = 0;
  let failed = false;
  steps.forEach((step, i) => {
    if ("skip" in step) {
      rows.push(step.skip);
      return;
    }
    const o = outcomes[i];
    if (!o) return;
    if ("error" in o) {
      const occupier = "skill" in step ? errorOccupier(s, step.skill.id, o.error) : undefined;
      rows.push({ label: step.label, outcome: t.failed, tone: "critical", reason: describeError(o.error), occupier });
      failed = true;
      return;
    }
    if ("skill" in step) {
      installed.add(`${scopeKey(step.scope)}:${step.skill.id}`);
      placed.push({ skill_id: step.skill.id, scope: step.scope });
      return;
    }
    groups++;
    const results = o.value;
    for (const r of results) {
      if (r.outcome !== GroupMemberOutcome.MemberInstalled) continue;
      installed.add(`${scopeKey(step.scope)}:${r.skill_id}`);
      placed.push({ skill_id: r.skill_id, scope: step.scope });
    }
    const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
    rows.push(...memberRows(s, skipped.filter((r) => step.overwrite || !step.untracked.has(r.skill_id))));
  });
  return { rows, installed: installed.size, groups, placed, failed };
}
