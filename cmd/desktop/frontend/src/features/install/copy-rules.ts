// 照着装(copy.tsx)里的判断：照着另一个 Agent、另一个项目装时可以照着谁、装哪些、哪些未纳管技能带不过去(agentCopy、projectCopy),
// 确认框里的勾选与要显示的内容(readingCopy、plansToRead、plansRead、copyView 与几个 toggle*),确认后逐处要做的事(copySteps)和结果怎么归并(copyResults)。
// 勾选怎么记(按作用域加技能或组)是这里的事，弹窗不碰;默认勾哪些、覆盖哪些见 lib/consent.ts。
// 不碰界面、不调技能库，单元测试见 copy-rules.test.ts。
import {
  GroupMemberOutcome,
  InstallPlanStatus,
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
import { copyable, copyDefaults, copyOverwriteCount, copyOverwrites, copyPlaceByDefault, type CopyPlanned } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { Attempt, LibraryError, Snapshot } from "@/lib/library";
import { editedHere, errorOccupier, placeKey, placesIn, sourceLatestHere } from "@/lib/installs";
import { sortSkills } from "@/lib/pages";
import { lastSegment, shortPath } from "@/lib/paths";
import { agentNameIn, mergeAgents, mergedScope, placeCount, sameFolderAt, scopeLabel, type AgentList } from "@/lib/places";
import { placeOf, projectName, scopeKey, scopeOf, type Place, type ScopeKey } from "@/lib/scope";
import { extraCopy, notTakenOver, repoOwned, unmanagedIn } from "@/lib/unmanaged";
import { memberRows, type ResultRow } from "@/lib/result-rows";
import { toggled } from "@/lib/utils";
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

// copyTarget 是照装到 to 时这一处装在谁的作用域上：目录列表相同、合成一处的几个 Agent 合成一处，落在代表的 Agent 上，
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
  const installed = new Set(placesIn(s, scopeKey(to)).map((p) => p.skill_id));
  const groups = s.groups.filter((g) => hasGroupAt(s, g, from.agent, placeOf(from)) && !hasGroupAt(s, g, to.agent, placeOf(to)));
  const there = placesIn(s, scopeKey(from)).filter((p) => !installed.has(p.skill_id));
  const skillsOf = (ids: SkillID[]) => sortSkills(s, s.skills.filter((x) => ids.includes(x.id)));
  const skills = skillsOf(there.filter((p) => p.direct).map((p) => p.skill_id));
  if (!groups.length && !skills.length) return [];
  const coming = new Set([...skills.map((x) => x.id), ...groups.flatMap((g) => g.members ?? [])]);
  const modified = there.filter((p) => coming.has(p.skill_id));
  const edited = skillsOf(modified.filter(editedHere).map((p) => p.skill_id));
  const newer = skillsOf(modified.filter(sourceLatestHere).map((p) => p.skill_id));
  const missing = !!s.projects.find((p) => p.id === placeOf(to))?.missing;
  return [{ to, groups, skills, edited, newer, missing }];
}

function copyFromAgent(s: Snapshot, from: AgentCode, to: AgentCode): CopyPlace[] {
  return [0, ...s.projects.map((p) => p.id)].flatMap((place) =>
    sameFolderAt(s, from, to, place) ? [] : copyPlace(s, scopeOf(from, place), scopeOf(copyTarget(s, to, place), place)),
  );
}

// copySources 是可以照着装的 Agent:to 以外、照装时有东西可装的(只在不在这台机器上的项目里有的不算),排在最前的是默认照着的。
// at 是从哪个作用域页打开的：从全局的(at 为 0)打开时装得最多的在前;从项目的打开时，在这个项目里可以照着装的项数
// (copyItems,和确认框里这一处标题上的数一样)最多的在前，一样多的(包括谁都没有)再按装得多少排。再一样多的按 Agent 列表顺序。
function copySources(s: Snapshot, to: AgentCode, at: Place): Agent[] {
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
const copyItems = (p: CopyPlace) => p.groups.length + p.skills.length;

// UncopiedUnmanaged 是照装带不过去的未纳管技能(见 splitUncopied)。
interface UncopiedUnmanaged {
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
function uncopiedUnmanaged(s: Snapshot, from: AgentCode, to: AgentCode): UncopiedUnmanaged {
  return splitUncopied(
    s.unmanaged.filter((u) => {
      const place = placeOf(u.scope);
      // 装着那个技能的地方多出来的一份(extraCopy)不用入库，照装时那个技能本来就带得过去。
      if (u.scope.agent !== from || sameFolderAt(s, from, to, place) || extraCopy(s, u)) return false;
      const toKey = scopeKey(scopeOf(copyTarget(s, to, place), place));
      return !folderNamesIn(s, toKey).includes(u.name);
    }),
  );
}

// folderNamesIn 是作用域的技能目录里已有的技能文件夹名：未纳管的和库装的。
function folderNamesIn(s: Snapshot, key: ScopeKey): string[] {
  return [...unmanagedIn(s, key).map((x) => x.name), ...placesIn(s, key).map((p) => lastSegment(p.folder))];
}

// projectAgentsUsing 是在项目里装了库里技能(直接安装或组安装)的 Agent,共用目录的合成一处(代表的 Agent)。
function projectAgentsUsing(s: Snapshot, project: ProjectID): Agent[] {
  const inProject = (scope: Scope, a: Agent) => scope.agent === a.code && placeOf(scope) === project;
  return mergeAgents(
    s,
    project,
    s.agents.filter((a) => s.skillScopes.some((p) => inProject(p.scope, a)) || s.groupInstalls.some((g) => inProject(g.scope, a))),
  );
}

// copyFromProject 是照着项目 from 装到项目 to:各 Agent(共用目录的合成一处)在 from 装的，装到 to 里的同一个 Agent。
function copyFromProject(s: Snapshot, from: ProjectID, to: ProjectID): CopyPlace[] {
  if (from === to) return [];
  return projectAgentsUsing(s, from).flatMap((a) => copyPlace(s, scopeOf(a.code, from), scopeOf(a.code, to)));
}

// projectInstalls 是项目里装的库里技能数，共用目录的几个 Agent 的同一个技能只算一次(和右栏项目行的数一致)。
function projectInstalls(s: Snapshot, project: ProjectID): number {
  return placeCount(s, s.skillScopes.filter((p) => placeOf(p.scope) === project));
}

// copyProjectSources 是可以照着装到 to 的其他已登记项目：装了库里技能、有东西可装的(只有空的组安装的不算),
// 装得最多的在前(默认选它),一样多的按项目列表顺序。
function copyProjectSources(s: Snapshot, to: ProjectID): RegisteredProject[] {
  return s.projects
    .filter((p) => projectInstalls(s, p.id) > 0 && copyFromProject(s, p.id, to).length > 0)
    .sort((a, b) => projectInstalls(s, b.id) - projectInstalls(s, a.id));
}

// uncopiedProjectUnmanaged 是项目 from 里照装带不过去的未纳管技能(按 splitUncopied 分开;to 里同一个 Agent 已有同名文件夹的不算)。
function uncopiedProjectUnmanaged(s: Snapshot, from: ProjectID, to: ProjectID): UncopiedUnmanaged {
  return splitUncopied(
    mergeAgents(s, from, s.agents).flatMap((a) => {
      const there = folderNamesIn(s, scopeKey(scopeOf(a.code, to)));
      return unmanagedIn(s, scopeKey(scopeOf(a.code, from))).filter((u) => !there.includes(u.name) && !extraCopy(s, u));
    }),
  );
}

// CopyKind 是一种照装：sources 是可以照着的(key 是 Agent 代号或项目 ID,排在最前的是默认照着的);选了照着谁(from,sources 里的 key)之后，
// places 是要装到的各处，left 是带不过去的未纳管技能(unmanaged 入库以后才能照着装，repo 归项目仓库管、随项目仓库走);
// heading 是一处的标题，取时才写，跟随当前界面语言;byDefault 是一处的各项打开时默认勾不勾。
export interface CopyKind {
  sources: { key: string; name: string }[];
  places: (from: string) => CopyPlace[];
  left: (from: string) => UncopiedUnmanaged;
  heading: (p: CopyPlace) => { name: string; detail?: string };
  byDefault: (p: CopyPlace) => boolean;
}

// agentCopy 是作用域页的「照着其他 Agent 装」:装到 Agent to,at 是从哪个作用域页打开的(全局为 0)。按地方分开列出;
// 从项目的作用域页打开时这个项目排在最前、默认只勾它(见 consent.ts 的 copyPlaceByDefault)。
export function agentCopy(s: Snapshot, to: AgentCode, at: Place): CopyKind {
  return {
    sources: copySources(s, to, at).map((a) => ({ key: a.code, name: a.name })),
    places: (from) => {
      const all = copyFromAgent(s, from as AgentCode, to);
      const here = (p: CopyPlace) => !!at && placeOf(p.to) === at;
      return [...all.filter(here), ...all.filter((p) => !here(p))];
    },
    left: (from) => uncopiedUnmanaged(s, from as AgentCode, to),
    heading: (p) => ({
      name: placeOf(p.to) ? projectName(s, placeOf(p.to)) : messages().model.global,
      detail: agentNameIn(s, p.to.agent, placeOf(p.to)),
    }),
    byDefault: (p) => copyPlaceByDefault(at, placeOf(p.to)),
  };
}

// projectCopy 是项目页的「照着其他项目装」:装到项目 to,按 Agent 分开列出，全局不在范围内;每一处都默认勾。
export function projectCopy(s: Snapshot, to: ProjectID): CopyKind {
  return {
    sources: copyProjectSources(s, to).map((p) => ({ key: String(p.id), name: projectName(s, p.id) })),
    places: (from) => copyFromProject(s, Number(from), to),
    left: (from) => uncopiedProjectUnmanaged(s, Number(from), to),
    heading: (p) => ({ name: agentNameIn(s, p.to.agent, to) }),
    byDefault: () => true,
  };
}

// ---------- 确认框 ----------

// 勾选的键：一处按装到的作用域区分，再加技能或组。只在这个文件里用。
const skillKey = (p: CopyPlace, id: number) => `s:${scopeKey(p.to)}:${id}`;
const groupKey = (p: CopyPlace, id: number) => `g:${scopeKey(p.to)}:${id}`;
const placeKeys = (p: CopyPlace) => [...p.groups.map((g) => groupKey(p, g.id)), ...p.skills.map((x) => skillKey(p, x.id))];

// Planned 是一项的安装计划(见 consent.ts 的 CopyPlanned):直接安装是一个技能的，组安装是各成员的;读不出时是技能库错误。
export type Planned = Exclude<CopyPlanned, { error: unknown }> | { error: LibraryError };

// canCheck 是这一项能不能勾：组安装读出计划就能勾，直接安装还要装得了(没被占用)。
const canCheck = (planned: Planned | undefined) => !!planned && ("members" in planned || ("plan" in planned && copyable(planned.plan.status)));

const state = Symbol("copy");

// CopyChoices 是确认框里的一份勾选，绑着选了照着谁之后要装的各处(places)与各处打开时默认勾不勾(byDefault):
// 换了照着谁就换一份(readingCopy),不会把上一份的勾选和计划套到新的各处上。里面怎么记弹窗不碰，只经下面几个函数读、改。
export type CopyChoices = {
  readonly [state]: {
    places: CopyPlace[];
    byDefault: (p: CopyPlace) => boolean;
    plans: ReadonlyMap<string, Planned> | null;
    checked: ReadonlySet<string>;
    overwrite: ReadonlySet<string>;
  };
};

// CopyItemRef 标识确认框里的一项(一处的一个组安装或一个直接安装的技能),弹窗只拿它交回 toggleItem、toggleOverwrite。
export type CopyItemRef = string & { readonly copyItem: unique symbol };

// readingCopy 是还没读出安装计划时的确认框：什么都没勾，标题的勾选框点不了、确认按钮点不了。
export function readingCopy(places: CopyPlace[], byDefault: (p: CopyPlace) => boolean): CopyChoices {
  return { [state]: { places, byDefault, plans: null, checked: new Set(), overwrite: new Set() } };
}

// CopyPlanRead 是要读的一项安装计划：哪个技能直接安装、或哪个组组安装到哪个作用域。项目不在这台机器上的那几处不读。
export type CopyPlanRead = { scope: Scope } & ({ skill: Skill } | { group: Group });

const readsOf = (c: CopyChoices) =>
  c[state].places
    .filter((p) => !p.missing)
    .flatMap((p) => [
      ...p.skills.map((x) => ({ key: skillKey(p, x.id), read: { scope: p.to, skill: x } as CopyPlanRead })),
      ...p.groups.map((g) => ({ key: groupKey(p, g.id), read: { scope: p.to, group: g } as CopyPlanRead })),
    ]);

// plansToRead 是打开确认框(或换了照着谁)之后要逐项读的安装计划，读到的按同样的先后交给 plansRead。
export const plansToRead = (c: CopyChoices): CopyPlanRead[] => readsOf(c).map((x) => x.read);

// plansRead 是读完安装计划之后的确认框：planned 与 plansToRead 一一对应。默认勾的那几处(byDefault)按 copyDefaults 勾上，
// 其余各处照样列出，点那一处的标题才按默认规则整处勾上;组安装的「覆盖」默认一个都不勾。
export function plansRead(c: CopyChoices, planned: readonly Planned[]): CopyChoices {
  const { places, byDefault } = c[state];
  const entries = readsOf(c).map((x, i): [string, Planned] => [x.key, planned[i]]);
  const meant = new Set(places.filter((p) => !p.missing && byDefault(p)).flatMap(placeKeys));
  const defaults = copyDefaults(entries.filter(([key]) => meant.has(key)));
  return { [state]: { places, byDefault, plans: new Map(entries), checked: defaults.checked, overwrite: defaults.overwrite } };
}

// CopySkillItem 是确认框里一个直接安装的技能：can 是能不能勾，untracked 是这里有不是库装的同名文件夹(勾上即覆盖,
// 值是文件夹，按界面惯例缩写),reason 是读不出计划或被占用、装不了的原因。
export interface CopySkillItem {
  ref: CopyItemRef;
  skill: Skill;
  on: boolean;
  can: boolean;
  untracked?: string;
  reason?: string;
}

// CopyGroupItem 是确认框里一个组安装：can 是读出了计划、能勾;coming 是要装过来的成员数(已装着的不算，读出计划才有);
// reason 是读不出计划的原因;overwrite 是勾没勾「覆盖」,untracked 是要装过来的成员里有不是库装的同名文件夹的(勾上组安装时问要不要覆盖),
// blocked 是被占用、装不了的成员。
export interface CopyGroupItem {
  ref: CopyItemRef;
  group: Group;
  on: boolean;
  can: boolean;
  coming?: number;
  reason?: string;
  overwrite: boolean;
  untracked: string[];
  blocked: string[];
}

// CopyPlaceView 是确认框里的一处：key 区分各处;on 是标题的勾选框勾不勾(有勾上的项就勾),mixed 是只勾了能勾的一部分(半勾);
// count 是这一处要装的项数(每个组安装、每个直接安装的技能各算一项)。项目不在这台机器上的一处(place.missing)没有可勾的项。
export interface CopyPlaceView {
  key: string;
  place: CopyPlace;
  on: boolean;
  mixed: boolean;
  count: number;
  groups: CopyGroupItem[];
  skills: CopySkillItem[];
}

// CopyView 是确认框要显示的全部内容。reading 是安装计划还没读完;confirm 是确认按钮上的直接安装的技能数、组安装数、
// 会覆盖几个不是库装的同名文件夹(见 consent.ts 的 copyOverwriteCount),ready 是能不能点;
// edited、newer 是勾上的项里会装过来的技能(直接安装勾上了，或随勾上的组安装装过来)里，原来那里被改过的、在 app 外更新到作者新版的，按名字去重。
export interface CopyView {
  reading: boolean;
  places: CopyPlaceView[];
  confirm: { skills: number; groups: number; overwrites: number; ready: boolean };
  edited: string[];
  newer: string[];
}

export function copyView(s: Snapshot, c: CopyChoices): CopyView {
  const { places, plans, checked, overwrite } = c[state];
  const skillItem = (p: CopyPlace, x: Skill): CopySkillItem => {
    const key = skillKey(p, x.id);
    const planned = plans?.get(key);
    const single = planned && "plan" in planned ? planned.plan : undefined;
    const reason =
      planned && "error" in planned ? describeError(planned.error) : single && !copyable(single.status) ? occupied(s, x.id, single).reason : undefined;
    return {
      ref: key as CopyItemRef,
      skill: x,
      on: checked.has(key),
      can: !!single && !reason,
      untracked: single && copyOverwrites(single.status) ? shortPath(s, single.folder) : undefined,
      reason,
    };
  };
  const groupItem = (p: CopyPlace, g: Group): CopyGroupItem => {
    const key = groupKey(p, g.id);
    const planned = plans?.get(key);
    const members = planned && "members" in planned ? planned.members : undefined;
    const coming = members?.filter((x) => x.status !== InstallPlanStatus.InstallAlreadyInstalled) ?? [];
    return {
      ref: key as CopyItemRef,
      group: g,
      on: checked.has(key),
      can: !!members,
      coming: members ? coming.length : undefined,
      reason: planned && "error" in planned ? describeError(planned.error) : undefined,
      overwrite: overwrite.has(key),
      untracked: coming.filter((x) => copyOverwrites(x.status)).map((x) => x.name),
      blocked: coming.filter((x) => !copyable(x.status)).map((x) => x.name),
    };
  };
  const placeView = (p: CopyPlace): CopyPlaceView => {
    const keys = placeKeys(p);
    const on = keys.filter((k) => checked.has(k)).length;
    const checkable = keys.filter((k) => canCheck(plans?.get(k))).length;
    return {
      key: scopeKey(p.to),
      place: p,
      on: on > 0,
      mixed: on > 0 && on < checkable,
      count: copyItems(p),
      groups: p.groups.map((g) => groupItem(p, g)),
      skills: p.skills.map((x) => skillItem(p, x)),
    };
  };
  const counted = [...checked];
  const skills = counted.filter((k) => k.startsWith("s:")).length;
  const coming = (p: CopyPlace, id: number) =>
    checked.has(skillKey(p, id)) || p.groups.some((g) => checked.has(groupKey(p, g.id)) && (g.members ?? []).includes(id));
  const names = (pick: (p: CopyPlace) => Skill[]) => [...new Set(places.flatMap((p) => pick(p).filter((x) => coming(p, x.id)).map((x) => x.name)))];
  return {
    reading: !plans,
    places: places.map(placeView),
    confirm: {
      skills,
      groups: checked.size - skills,
      overwrites: plans ? copyOverwriteCount(plans, checked, overwrite) : 0,
      ready: !!plans && checked.size > 0,
    },
    edited: names((p) => p.edited),
    newer: names((p) => p.newer),
  };
}

const withChecked = (c: CopyChoices, change: Partial<Pick<CopyChoices[typeof state], "checked" | "overwrite">>): CopyChoices => ({
  [state]: { ...c[state], ...change },
});

// toggleItem 勾上或取消一项;组安装的「覆盖」不跟着变，再勾上组安装时还是原来的样子。
export const toggleItem = (c: CopyChoices, ref: CopyItemRef) => withChecked(c, { checked: toggled(c[state].checked, ref) });

// toggleOverwrite 勾上或取消一个组安装的「覆盖」。
export const toggleOverwrite = (c: CopyChoices, ref: CopyItemRef) => withChecked(c, { overwrite: toggled(c[state].overwrite, ref) });

// togglePlace 是点一处的标题：有勾上的就整处取消;一个都没勾时按 copyDefaults 整处勾上(不管这一处打开时默认勾不勾，
// 不替用户勾上要覆盖的),读不出计划的项不勾。还没读完安装计划时不变。
export function togglePlace(c: CopyChoices, place: CopyPlace): CopyChoices {
  const { plans, checked } = c[state];
  if (!plans) return c;
  const keys = placeKeys(place);
  const next = new Set(checked);
  const any = keys.some((k) => next.has(k));
  const entries = keys.flatMap((k): [string, Planned][] => {
    const v = plans.get(k);
    return v ? [[k, v]] : [];
  });
  const defaults = copyDefaults(entries).checked;
  for (const k of keys) {
    if (any) next.delete(k);
    else if (defaults.has(k)) next.add(k);
  }
  return withChecked(c, { checked: next });
}

// ---------- 确认后 ----------

// CopyStep 是确认后的一步:skip 是不装、直接写进结果的一行(项目不在这台机器上、读不出计划、被占用);
// group 是一次组安装(overwrite 是勾了"覆盖",untracked 是其中有不是库装的同名文件夹的成员),skill 是一次直接安装。label 是结果里的写法。
export type CopyStep =
  | { skip: ResultRow }
  | { group: Group; scope: Scope; label: string; overwrite: boolean; untracked: Set<SkillID> }
  | { skill: Skill; scope: Scope; label: string; overwrite: boolean };

// copySteps 按勾选逐处要做的事：只算默认勾的那几处和用户勾了东西的那几处，其余各处装不了、读不出计划的不算没装上;
// 每一处先组安装，再直接安装;没勾的项不装，读不出计划、被占用的照样写进结果。还没读完安装计划时什么都不做。
export function copySteps(s: Snapshot, c: CopyChoices): CopyStep[] {
  const { places, byDefault, plans, checked, overwrite } = c[state];
  if (!plans) return [];
  const t = messages().copy;
  const a = messages().actions;
  const skip = (label: string, reason: string, occupier?: SkillID): CopyStep => ({ skip: { label, outcome: a.memberSkipped, tone: "warn", reason, occupier } });
  const meant = places.filter((p) => byDefault(p) || placeKeys(p).some((k) => checked.has(k)));
  return meant.flatMap((p): CopyStep[] => {
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
// 跳过的是 undefined。
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
      installed.add(placeKey({ skill_id: step.skill.id, scope: step.scope }));
      placed.push({ skill_id: step.skill.id, scope: step.scope });
      return;
    }
    groups++;
    const results = o.value;
    for (const r of results) {
      if (r.outcome !== GroupMemberOutcome.MemberInstalled) continue;
      installed.add(placeKey({ skill_id: r.skill_id, scope: step.scope }));
      placed.push({ skill_id: r.skill_id, scope: step.scope });
    }
    const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
    rows.push(...memberRows(s, skipped.filter((r) => step.overwrite || !step.untracked.has(r.skill_id))));
  });
  return { rows, installed: installed.size, groups, placed, failed };
}
