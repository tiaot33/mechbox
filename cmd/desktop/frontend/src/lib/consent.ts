// 默认勾选与写之前要说明的地方：守住"用户没有点头，Agent 用的技能就不变"(ADR 0010)的规则都在这里，
// 是不碰界面、不调技能库的纯函数，单元测试见 consent.test.ts。
// - 用户改过的地方、文件夹不在了的地方默认不动，勾上(或选"装回去")才覆盖、才装回去。
// - 用户改过的技能有变化时默认不勾，跳过的改动、来源里已删除的技能、推测出的改名都不默认勾。
//   更新页不打开更新计划直接「更新」「全部更新」时按同一条规则，只更新到默认会勾的那些(updatedByDefault)。
// - 库写到符号链接那一处、删掉被别处链接指着的文件夹之前，先说出来。
// - 卸载、移除会删掉被手动改过的文件夹时，先说出是哪几处，用户确认后才删。
// - 整批入库时，归项目仓库管的、指向 Agent 目录以外的、库里技能在来源里的新版默认不接，用户自己的照常默认勾上。
// - 更新时，在 app 外已经更新到作者新版的那一处照常勾上、不算覆盖用户的修改。
// - 照着另一个 Agent、另一个项目装时，有不是库装的同名文件夹的默认不覆盖，勾上才覆盖。
// - 从项目的作用域页照着另一个 Agent 装时，全局和其他项目默认不勾，只装这个项目。
import {
  InstallPlanStatus,
  PlannedChange,
  type Candidate,
  type InstallPlan,
  SkillScopeStatus,
  type PlannedUnmanagedSkill,
  type ReinstallRequest,
  type Scope,
  type Skill,
  type SkillID,
  type SkillScopeInspection,
  type UnmanagedSkill,
  type UpdatePlan,
} from "@bindings/internal/library";
import { canInstall, editedHere, folderKey, needsReinstall } from "./installs";
import { notTakenOver } from "./unmanaged";
import { scopeKey } from "./scope";
import { sourceState } from "./source-state";

// placeKey 标识一处安装：哪个技能装在哪个作用域。
export const placeKey = (p: { skill_id: SkillID; scope: Scope }) => `${p.skill_id}|${scopeKey(p.scope)}`;

// ---------- 更新计划 ----------

// defaultSkills 是默认勾选：有变化且没跳过的全勾;给了 select(从技能发起、从跳过记录找回)时只勾这几个。
// 改名推测都不默认勾，等用户回答"是";用户改过的技能(edited)有变化时也不默认勾，用户自己勾上才算同意换掉修改。
export function defaultSkills(plan: UpdatePlan, select?: SkillID[]): Set<SkillID> {
  const skills = (plan.skills ?? []).filter((p) => !(p.edited && p.change === PlannedChange.PlannedChanged));
  if (select) {
    return new Set(skills.filter((p) => select.includes(p.skill.id) && !p.renamed_to && p.change !== PlannedChange.PlannedDeleted).map((p) => p.skill.id));
  }
  return new Set(skills.filter((p) => p.change === PlannedChange.PlannedChanged && !p.skipped).map((p) => p.skill.id));
}

// edited 是用户在这里改过的一处，和界面上说「被手动改过」的是同一批(lib/installs 的 editedHere)。
const edited = editedHere;

// defaultInstallOn 是装出去的地方的默认勾选：用户改过的、文件夹不在了的(可能是用户有意删掉的)不勾，勾上才覆盖、才装回去。
// 在 app 外已经更新到作者新版的照常勾上：库换成这一版之后它就是当前版本，不会被覆盖。
export const defaultInstallOn = (p: SkillScopeInspection) => canInstall(p) && !edited(p) && p.status !== SkillScopeStatus.SkillScopeMissing;

// chosenInstallOn 是更新计划、用目录里的修改更新的弹窗里一处勾没勾:choice 是用户点过的(按文件夹，几个 Agent 共用一个文件夹时只能一起换),
// 没点过的按 defaultInstallOn;项目不在这台机器上的装不到，不勾。
export const chosenInstallOn = (choice: ReadonlyMap<string, boolean>) => (p: SkillScopeInspection) =>
  canInstall(p) && (choice.get(folderKey(p)) ?? defaultInstallOn(p));

// consented 是勾上即同意的地方：用户改过的覆盖，文件夹不在了的装回去。作者新版的那一处不用同意覆盖。
const consented = (p: SkillScopeInspection) => edited(p) || p.status === SkillScopeStatus.SkillScopeMissing;

// updatedByDefault 是不打开更新计划、按默认勾选更新时会更新到的技能：有新版本、计入角标(没跳过),库里的当前版本没有用户的修改。
// 和 defaultSkills 是同一条规则，这里按快照里的检查结论算：更新页的「更新」「全部更新」只在有这样的技能时才能直接做，
// 都是会换掉用户修改的就只打开更新计划，让用户自己勾。
export function updatedByDefault(skill: Skill): boolean {
  const state = sourceState(skill);
  return state.todo && state.kind === "newVersion" && !state.replacesEdits;
}

// plannedInstalls 是勾选的技能装出去的各处;更新计划只列有变化(或推测出改名)的技能。
export function plannedInstalls(plan: UpdatePlan, checked: Set<SkillID>): SkillScopeInspection[] {
  return (plan.installs ?? []).filter((p) => checked.has(p.skill_id));
}

// UpdateInstalls 是一次更新要换的地方：places 是勾选的技能装出去的各处，requests 是其中要换的(能装到、勾上的);
// 用户改过的、文件夹不在了的带 overwrite(勾上即同意)。keptEdited / keptMissing 是没勾、保持原样的文件夹数。
export interface UpdateInstalls {
  places: SkillScopeInspection[];
  written: (p: SkillScopeInspection) => boolean;
  requests: ReinstallRequest[];
  keptEdited: number;
  keptMissing: number;
}

// updateInstalls 按更新计划里的勾选算出要换的地方;更新计划只列有变化(或推测出改名)的技能装出去的地方。
export function updateInstalls(plan: UpdatePlan, checked: Set<SkillID>, installOn: (p: SkillScopeInspection) => boolean): UpdateInstalls {
  const places = plannedInstalls(plan, checked);
  const written = (p: SkillScopeInspection) => canInstall(p) && installOn(p);
  const kept = (status: SkillScopeStatus) =>
    new Set(places.filter((p) => p.status === status && !p.source_latest && !installOn(p)).map(folderKey)).size;
  return {
    places,
    written,
    requests: places.filter(written).map((p) => ({ skill_id: p.skill_id, scope: p.scope, overwrite: consented(p) })),
    keptEdited: kept(SkillScopeStatus.SkillScopeModified),
    keptMissing: kept(SkillScopeStatus.SkillScopeMissing),
  };
}

// refreshRequests 是本地目录内容没变时"重新读取"要补换的地方：不是当前版本、能装到的几处里，用户改过的、文件夹不在了的不动，
// 其余换成当前版本。keptEdited / keptMissing 是没动的文件夹数。
export function refreshRequests(inspected: SkillScopeInspection[]) {
  const behind = inspected.filter(needsReinstall);
  const kept = (status: SkillScopeStatus) => new Set(behind.filter((p) => p.status === status).map(folderKey)).size;
  return {
    requests: behind
      .filter((p) => !consented(p))
      .map((p): ReinstallRequest => ({ skill_id: p.skill_id, scope: p.scope, overwrite: false })),
    keptEdited: kept(SkillScopeStatus.SkillScopeModified),
    keptMissing: kept(SkillScopeStatus.SkillScopeMissing),
  };
}

// ---------- 重新安装 ----------

export type MissingChoice = "keep" | "putBack" | "uninstall";

// ReinstallChoices 是确认框里的选择:被改过的勾选了哪几处覆盖(overwriteKey),文件夹不在了的每个文件夹选了什么(folderKey)。
export interface ReinstallChoices {
  overwrite: Set<string>;
  missing: Map<string, MissingChoice>;
}

// overwriteKey 是被改过的一处在确认框里的勾选:项目里共用一个文件夹的几处合成一行，按文件夹勾选;其余逐处勾选。
export const overwriteKey = (p: SkillScopeInspection) => (p.scope.project_id ? folderKey(p) : placeKey(p));

// ReinstallTargets 是现查之后要重新安装的几处，以及其中要问用户的：被改过的、文件夹不在了的。
// 只点了一处(single)时文件夹不在了也直接装回去，不再问。
export function reinstallTargets(inspected: SkillScopeInspection[], single: boolean) {
  const targets = inspected.filter(needsReinstall);
  return {
    targets,
    modified: targets.filter((p) => p.status === SkillScopeStatus.SkillScopeModified),
    missing: single ? [] : targets.filter((p) => p.status === SkillScopeStatus.SkillScopeMissing),
  };
}

// planReinstall 按确认框里的选择算出要重新安装的几处与要卸载的文件夹:被改过的只覆盖勾上的，文件夹不在了的默认不动，
// 选"装回去"的才装回去、选"卸载"的才卸载(共用一个文件夹的几处只卸载一处)。kept* 是保持原样的数目，完成提示里说。
// reinstallsNothing 是确认框里这样选下去什么都不会做：没有要重新安装的，也没有要卸载的(确认按钮点不了)。
export function reinstallsNothing(targets: SkillScopeInspection[], single: boolean, chosen: ReinstallChoices): boolean {
  const r = planReinstall(targets, single, chosen);
  return !r.requests.length && !r.uninstalls.length;
}

export function planReinstall(targets: SkillScopeInspection[], single: boolean, chosen: ReinstallChoices) {
  const choice = (p: SkillScopeInspection) => chosen.missing.get(folderKey(p)) ?? "keep";
  const wanted = (p: SkillScopeInspection) => {
    if (p.status === SkillScopeStatus.SkillScopeModified) return chosen.overwrite.has(overwriteKey(p));
    if (p.status === SkillScopeStatus.SkillScopeMissing) return single || choice(p) === "putBack";
    return true;
  };
  const missing = single ? [] : targets.filter((p) => p.status === SkillScopeStatus.SkillScopeMissing);
  const modified = targets.filter((p) => p.status === SkillScopeStatus.SkillScopeModified);
  // 重新安装是换回库里的当前版本：勾上的被改过的一律带覆盖，包括在 app 外更新到作者新版的那一处。
  const requests: ReinstallRequest[] = targets.filter(wanted).map((p) => ({
    skill_id: p.skill_id,
    scope: p.scope,
    overwrite: p.status === SkillScopeStatus.SkillScopeModified || p.status === SkillScopeStatus.SkillScopeMissing,
  }));
  return {
    requests,
    uninstalls: [...new Map(missing.filter((p) => choice(p) === "uninstall").map((p) => [folderKey(p), p])).values()],
    keptModified: new Set(modified.filter((p) => !wanted(p)).map(overwriteKey)).size,
    keptMissing: new Set(missing.filter((p) => choice(p) === "keep").map(folderKey)).size,
  };
}

// ---------- 符号链接 ----------

// 库写到是符号链接的那一处时写成独立的一份;覆盖或删除一处时，别处没一起动的链接若指着它，先给那一处换上独立的一份、
// 保持它现在读到的内容。写之前、删之前都在确认里说出来。

export type Detached = { place: SkillScopeInspection; by: Scope[] };

// detachedBy 是 touched 这几处(要覆盖或删除的)被别处的链接指着、而那一处不在 touched 里的:库会先给那一处换上独立的一份。
// 共用一个文件夹的几处只算一次。
export function detachedBy(touched: SkillScopeInspection[]): Detached[] {
  const keys = new Set(touched.map(placeKey));
  const folders = new Set<string>();
  return touched.flatMap((place) => {
    const folder = `${place.skill_id}|${place.folder}`;
    if (folders.has(folder)) return [];
    folders.add(folder);
    const by = (place.linked_by ?? []).filter((scope) => !keys.has(placeKey({ skill_id: place.skill_id, scope })));
    return by.length ? [{ place, by }] : [];
  });
}

// linkWrites 是写这几处(written)时和符号链接有关的两种情况:links 是写的就是链接(会换成独立的一份),
// kept 是别处没一起写的链接指着要写的地方(先换成独立的一份，保持原来的内容)。
export function linkWrites(places: SkillScopeInspection[], written: (p: SkillScopeInspection) => boolean) {
  const chosen = places.filter(written);
  return { links: chosen.filter((p) => p.link_target), kept: detachedBy(chosen) };
}

// ---------- 删除文件夹 ----------

// lostEdits 是删掉 deleted 这几处的文件夹时，修改会跟着丢掉的地方：被手动改过的(在 app 外更新到作者新版的不算，
// 那是作者的内容)。是符号链接的那一处只删链接，链接指着的文件夹和里面的修改都还在，不算。共用一个文件夹的几处只算一处。
export function lostEdits(deleted: SkillScopeInspection[]): SkillScopeInspection[] {
  const folders = new Set<string>();
  return deleted.filter((p) => {
    if (!edited(p) || p.link_target || folders.has(folderKey(p))) return false;
    folders.add(folderKey(p));
    return true;
  });
}

// discardedEdits 是确认里说过修改会丢掉的那几个文件夹(和 lostEdits 列出的是同一份),交给技能库的 DiscardEdits:
// 技能库删之前只放过这几个，其余确认之后才被改过的一个都不删，再问一次。选了保留文件夹时技能库不删、也不核对。
export const discardedEdits = (deleted: SkillScopeInspection[]): string[] => lostEdits(deleted).map((p) => p.folder);

// ---------- 入库预览 ----------

// defaultCandidates 是入库预览里默认勾上的候选(按名字):还没入库的都勾(从新技能提示进来时只勾新技能);
// 库里已经有同名技能(别的来源、或没有来源)的不勾：入库后库里会多出一个同名的，用户看到提醒、自己勾上才算知情。
export function defaultCandidates(candidates: Candidate[], libraryNames: ReadonlySet<string>, onlyNew: boolean): Set<string> {
  return new Set(candidates.filter((c) => !c.fetched && (!onlyNew || c.new) && !libraryNames.has(c.name)).map((c) => c.name));
}

// ---------- 整批入库 ----------

// notOwn 是默认不接的一处：提交在项目仓库里、记在项目 skills-lock.json 里，或指向 Agent 目录以外(见 lib/unmanaged 的 notTakenOver)。
const notOwn = (f: UnmanagedSkill) => !notTakenOver(f);

// fetchPlan 是整批入库确认里的各条与默认勾选。库把内容一致的几处合成一条，其中一处默认不接整条就不勾;
// 这里把默认不接的几处拆成单独一条，用户自己的那几处照常默认勾上(全局和项目一起看时，全局那份不被项目里团队的那份拖着)。
// 同名冲突时默认勾用户自己的几条里最近修改的那条;都不是用户自己的就都不勾。
export function fetchPlan(plan: PlannedUnmanagedSkill[]): PlannedUnmanagedSkill[] {
  const split = plan.flatMap((p) => {
    const own = (p.folders ?? []).filter((f) => !notOwn(f));
    const others = (p.folders ?? []).filter(notOwn);
    if (p.error || !own.length || !others.length) return [p];
    return [own, others].map((folders) => ({
      ...p,
      folders,
      in_repository: folders.some((f) => f.in_repository),
      in_project_lock: folders.some((f) => f.in_project_lock),
      link_outside: folders.some((f) => f.link_outside),
    }));
  });
  // 库里技能在来源里的新版(在 app 外更新过)不用入库，用更新换上，默认不勾。
  const ownEntry = (p: PlannedUnmanagedSkill) => !p.error && notTakenOver(p) && !p.source_latest_of;
  const time = (p: PlannedUnmanagedSkill) => (p.modified_at ? Date.parse(p.modified_at) : 0);
  const newest = new Map<string, PlannedUnmanagedSkill>();
  for (const p of split) {
    const cur = newest.get(p.name);
    if (ownEntry(p) && p.conflict && (!cur || time(p) > time(cur))) newest.set(p.name, p);
  }
  return split.map((p) => ({ ...p, selected: ownEntry(p) && (!p.conflict || newest.get(p.name) === p) }));
}

// sameFolderElsewhere 是一条计划入库时库会一并记下的别处：各处文件夹的 same_folder 里、不在这一条里的作用域
// (经符号链接是同一个文件夹，见 UnmanagedSkill.same_folder)。确认前写出来，不让用户以为只动了勾上的那几处。
export function sameFolderElsewhere(p: PlannedUnmanagedSkill): Scope[] {
  const own = new Set((p.folders ?? []).map((f) => scopeKey(f.scope)));
  const elsewhere = new Map<string, Scope>();
  for (const f of p.folders ?? []) for (const sc of f.same_folder ?? []) if (!own.has(scopeKey(sc))) elsewhere.set(scopeKey(sc), sc);
  return [...elsewhere.values()];
}

// toggleFetchGroup 是整批入库确认里一组(同一来源，或没有来源)标题上的勾选：组里有勾上的就整组取消;
// 一个都没勾时整组勾上，默认不接的(在项目仓库里的、指向 Agent 目录以外的)也勾上：用户自己点的，提醒照常写出来。
export function toggleFetchGroup(picked: ReadonlySet<number>, members: number[]): Set<number> {
  const next = new Set(picked);
  const any = members.some((i) => next.has(i));
  for (const i of members) {
    if (any) next.delete(i);
    else next.add(i);
  }
  return next;
}

// ---------- 照着装(照着另一个 Agent、另一个项目) ----------

// copyable 是照装时这一项能不能勾：被另一个技能占用的、组里同名的装不了。
export const copyable = (status: InstallPlanStatus) =>
  status !== InstallPlanStatus.InstallOccupied && status !== InstallPlanStatus.InstallDuplicate;

// copyByDefault 是照装时默认勾的那几处里(见 copyPlaceByDefault)默认勾上的：能装的都勾，有不是库装的同名文件夹的不勾，勾上才覆盖(copyOverwrites)。
// 组安装整组默认勾上，组员里有这种文件夹的默认跳过，用户勾上"覆盖"才覆盖。
export const copyByDefault = (status: InstallPlanStatus) => copyable(status) && status !== InstallPlanStatus.InstallUntracked;

// copyOverwrites 是勾上即同意覆盖的：这里有不是库装的同名文件夹。
export const copyOverwrites = (status: InstallPlanStatus) => status === InstallPlanStatus.InstallUntracked;

// copyPlaceByDefault 是照着其他 Agent 装时，某一处(place,全局为 0)的各项打开时默认勾不勾，勾哪些仍按 copyDefaults。
// at 是从哪个作用域页打开的：从全局的(at 为 0)打开时每一处都勾;从项目的打开时只勾这个项目，用户想的是这个项目：
// 全局装上的在所有项目里都看得到，其他项目仓库里会多出未跟踪的文件夹。其余各处照样列出，点那一处的标题按默认规则整处勾上。
export const copyPlaceByDefault = (at: number, place: number) => !at || place === at;

// CopyPlanned 是照装时一项的安装计划：直接安装是一个技能的，组安装是各成员的;读不出时是错误。
export type CopyPlanned = { plan: InstallPlan } | { members: InstallPlan[] } | { error: unknown };

// copyDefaults 是一组项按默认规则的勾选：确认框打开时用于默认勾的那几处(哪几处见 copyPlaceByDefault),
// 点一处的标题整处勾上时用于那一处。checked 是默认勾上的项(组安装整组勾上，直接安装按 copyByDefault,读不出计划的不勾);
// overwrite 是勾了"覆盖"的组安装，默认一个都没有：组员里有不是库装的同名文件夹的默认跳过。
export function copyDefaults(entries: [string, CopyPlanned][]): { checked: Set<string>; overwrite: Set<string> } {
  const checked = entries.flatMap(([key, v]) => ("members" in v || ("plan" in v && copyByDefault(v.plan.status)) ? [key] : []));
  return { checked: new Set(checked), overwrite: new Set() };
}

// copyOverwriteCount 是按勾选会覆盖几个不是库装的同名文件夹：勾上的直接安装里有的，加上勾了"覆盖"的组安装里组员有的。
export function copyOverwriteCount(entries: Map<string, CopyPlanned>, checked: Set<string>, overwrite: Set<string>): number {
  let n = 0;
  for (const [key, v] of entries) {
    if (!checked.has(key)) continue;
    if ("plan" in v && copyOverwrites(v.plan.status)) n++;
    if ("members" in v && overwrite.has(key)) n += v.members.filter((x) => copyOverwrites(x.status)).length;
  }
  return n;
}
