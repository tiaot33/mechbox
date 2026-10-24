// 默认勾选与写之前要说明的地方：守住"用户没有点头，Agent 用的技能就不变"(ADR 0010)的规则都在这里，
// 是不碰界面、不调技能库的纯函数，单元测试见 consent.test.ts。
// - 用户改过的地方、文件夹不在了的地方默认不动，勾上(或选"装回去")才覆盖、才装回去。
// - 用户改过的技能有变化时默认不勾，跳过的改动、来源里已删除的技能、推测出的改名都不默认勾。
// - 库写到符号链接那一处、删掉被别处链接指着的文件夹之前，先说出来。
import {
  PlannedChange,
  SkillScopeStatus,
  type ReinstallRequest,
  type Scope,
  type SkillID,
  type SkillScopeInspection,
  type UpdatePlan,
} from "@bindings/internal/library";
import { folderKey, needsReinstall, scopeKey } from "./model";

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

// canInstall 是能装到的地方：项目不在这台机器上的不能勾。
export const canInstall = (p: SkillScopeInspection) => p.status !== SkillScopeStatus.SkillScopeRootMissing;

// defaultInstallOn 是装出去的地方的默认勾选：用户改过的、文件夹不在了的(可能是用户有意删掉的)不勾，勾上才覆盖、才装回去。
export const defaultInstallOn = (p: SkillScopeInspection) =>
  canInstall(p) && p.status !== SkillScopeStatus.SkillScopeModified && p.status !== SkillScopeStatus.SkillScopeMissing;

// consented 是勾上即同意的地方：用户改过的覆盖，文件夹不在了的装回去。
export const consented = (p: SkillScopeInspection) =>
  p.status === SkillScopeStatus.SkillScopeModified || p.status === SkillScopeStatus.SkillScopeMissing;

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
  const kept = (status: SkillScopeStatus) => new Set(places.filter((p) => p.status === status && !installOn(p)).map(folderKey)).size;
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
  const behind = inspected.filter((p) => p.status !== SkillScopeStatus.SkillScopeCurrent && canInstall(p));
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
export function planReinstall(targets: SkillScopeInspection[], single: boolean, chosen: ReinstallChoices) {
  const choice = (p: SkillScopeInspection) => chosen.missing.get(folderKey(p)) ?? "keep";
  const wanted = (p: SkillScopeInspection) => {
    if (p.status === SkillScopeStatus.SkillScopeModified) return chosen.overwrite.has(overwriteKey(p));
    if (p.status === SkillScopeStatus.SkillScopeMissing) return single || choice(p) === "putBack";
    return true;
  };
  const missing = single ? [] : targets.filter((p) => p.status === SkillScopeStatus.SkillScopeMissing);
  const modified = targets.filter((p) => p.status === SkillScopeStatus.SkillScopeModified);
  const requests: ReinstallRequest[] = targets.filter(wanted).map((p) => ({
    skill_id: p.skill_id,
    scope: p.scope,
    overwrite: consented(p),
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
