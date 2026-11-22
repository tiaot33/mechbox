// 安装与卸载流程(install.tsx)里的判断：装到哪几处，看过安装计划后哪几处跳过、哪几处覆盖，结果怎么归并;
// 卸载时哪几个文件夹会删、哪些是全局共用的，要不要先确认。都是不碰界面、不调技能库的纯函数，单元测试见 install-rules.test.ts。
// 默认勾选与写之前的说明(别处链接指着、被手动改过)见 lib/consent.ts。
import {
  ErrorCode,
  GroupMemberOutcome,
  InstallPlanStatus,
  type GroupID,
  type GroupMemberResult,
  type InstallPlan,
  type MissingMention,
  type Scope,
  type Skill,
  type SkillID,
  type SkillInScope,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { detachedBy, lostEdits } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { installable, missingNames } from "@/lib/mentions";
import { errorOccupier, folderKey, folderPlaces, occupierOf, skillScopeIn } from "@/lib/installs";
import { noSource, sourceLabel } from "@/lib/source";
import { placeOf, scopeKey, skillName } from "@/lib/scope";
import { scopeLabel, sharedWithNote } from "@/lib/places";
import { shortPath } from "@/lib/paths";
import { placedBy, type ResultRow } from "@/components/app/result-rows";

// occupied 是被占用、装不上的一处:reason 说明原因，同名的另一个技能(或这个技能的另一个作用域)已装在那个文件夹，用来源区分是哪一个;
// occupier 是占着的另一个技能，界面上能点开它(见 occupierOf)。
export function occupied(s: Snapshot, id: SkillID, plan: InstallPlan): { reason: string; occupier?: SkillID } {
  const t = messages().actions;
  const folder = shortPath(s, plan.folder);
  if (plan.occupier_skill_id === id) return { reason: t.occupiedSelf(folder) };
  const occupier = occupierOf(s, id, plan.occupier_skill_id);
  const skill = s.skills.find((x) => x.id === occupier);
  const reason = !skill ? t.occupiedOther(folder) : noSource(skill.source) ? t.occupiedNoSource(folder) : t.occupiedSource(sourceLabel(s, skill.source), folder);
  return { reason, occupier };
}

// ---------- 装好之后的提示 ----------

// mentionedMissing 是装好之后的提示要说的:missing 是装上的这几处提到、在那里找不到的技能;skills 是有找不到的技能数，
// names 是找不到的名字;targets 是库里有、那里还没有安装记录的(有记录、文件夹不在了的要重新安装，不在这里装),提示上的按钮一起装上;
// count 是 targets 里的技能数。
export function mentionedMissing(s: Snapshot, missing: MissingMention[]) {
  const targets = installable(missing).filter((p) => !skillScopeIn(s, p.skill_id, scopeKey(p.scope)));
  return {
    skills: new Set(missing.map((m) => m.skill_id)).size,
    names: missingNames(missing),
    targets,
    count: new Set(targets.map((p) => p.skill_id)).size,
  };
}

// ---------- 逐处直接安装 ----------

// InstallItem 是逐处直接安装的一处：一个技能装到一个 Agent。
export type InstallItem = { skill: Skill; scope: Scope };

// notDirectIn 是把 skills 逐个直接安装到 scopes 时要装的各处：已经直接安装在那里的不再装。先按 scopes,再按 skills 的先后。
export function notDirectIn(s: Snapshot, skills: Skill[], scopes: Scope[]): InstallItem[] {
  return scopes.flatMap((scope) => skills.filter((x) => !skillScopeIn(s, x.id, scopeKey(scope))?.direct).map((skill) => ({ skill, scope })));
}

// PlanRead 是逐处看安装计划的结果：读出来的计划，或读不出时的技能库错误。
export type PlanRead = { plan: InstallPlan } | { error: LibraryError };

// InstallPlans 是逐处看过安装计划之后:several 是不是装到几个 Agent(询问与结果里每一处写出是哪个 Agent,label 是一处的写法);
// planned 是读出了计划的各处，untracked 是其中有不是库装的同名文件夹、要合成一次询问的;unread 是看计划就出错的那几处的结果行，算失败。
export interface InstallPlans {
  several: boolean;
  label: (x: InstallItem) => string;
  planned: (InstallItem & { plan: InstallPlan })[];
  untracked: (InstallItem & { plan: InstallPlan })[];
  unread: ResultRow[];
}

// readInstallPlans 按逐处看安装计划的结果(reads 与 items 一一对应)分出读出了计划的、要问的和看计划就出错的。
export function readInstallPlans(s: Snapshot, items: InstallItem[], reads: PlanRead[]): InstallPlans {
  const t = messages().actions;
  const several = new Set(items.map((x) => scopeKey(x.scope))).size > 1;
  const label = (x: InstallItem) => (several ? `${x.skill.name} · ${scopeLabel(s, x.scope)}` : x.skill.name);
  const unread: ResultRow[] = [];
  const planned = items.flatMap((x, i) => {
    const r = reads[i];
    if ("plan" in r) return [{ ...x, plan: r.plan }];
    unread.push({ label: label(x), outcome: t.failed, tone: "critical", reason: describeError(r.error) });
    return [];
  });
  return { several, label, planned, untracked: planned.filter((x) => x.plan.status === InstallPlanStatus.InstallUntracked), unread };
}

// InstallStep 是逐处安装的一步:skip 是不装、直接写进结果的一行(被别的技能占用，或有同名文件夹而用户选了跳过);
// 否则装这一处,overwrite 是覆盖不是库装的同名文件夹。label 是这一处在结果里的写法。
export type InstallStep = { item: InstallItem; label: string } & ({ skip: ResultRow } | { overwrite: boolean });

// installSteps 是按安装计划和用户对同名文件夹的选择(overwrite 为 true 是覆盖，否则跳过)逐处要做的事，按 planned 的先后。
export function installSteps(s: Snapshot, plans: InstallPlans, overwrite: boolean): InstallStep[] {
  const t = messages().actions;
  return plans.planned.map(({ plan, ...item }): InstallStep => {
    const label = plans.label(item);
    const untracked = plan.status === InstallPlanStatus.InstallUntracked;
    if (plan.status === InstallPlanStatus.InstallOccupied) {
      return { item, label, skip: { label, outcome: t.memberSkipped, tone: "warn", ...occupied(s, item.skill.id, plan) } };
    }
    if (untracked && !overwrite) {
      return { item, label, skip: { label, outcome: t.memberSkipped, tone: "warn", reason: t.untrackedBody(shortPath(s, plan.folder), plan.name) } };
    }
    return { item, label, overwrite: untracked };
  });
}

// installResults 归并逐处安装的结果:errors 与 steps 一一对应，装成的是 null、装不上的是错误，跳过的和没做到的是 undefined。
// rows 是结果列表(看计划就出错的在前，其余按 steps 的先后);placed 是装上的各处，installed 是装上了几处;
// failed 是有没装成的(看计划就出错的也算),顶栏画叉。计划算的时候还没被占、执行时被同一批里先装的同名技能占上了的，
// 错误里有占着的那个，结果里照样能点开。
export function installResults(s: Snapshot, plans: InstallPlans, steps: InstallStep[], errors: (LibraryError | null | undefined)[]) {
  const t = messages().actions;
  const rows = [...plans.unread];
  const placed: SkillInScope[] = [];
  let failed = plans.unread.length > 0;
  steps.forEach((step, i) => {
    if ("skip" in step) {
      rows.push(step.skip);
      return;
    }
    const err = errors[i];
    if (err === undefined) return;
    if (err === null) {
      rows.push({ label: step.label, outcome: t.memberInstalled, tone: "plain" });
      placed.push({ skill_id: step.item.skill.id, scope: step.item.scope });
      return;
    }
    rows.push({ label: step.label, outcome: t.failed, tone: "critical", reason: describeError(err), occupier: errorOccupier(s, step.item.skill.id, err) });
    failed = true;
  });
  return { rows, placed, installed: placed.length, failed };
}

// ---------- 组安装 ----------

// groupInstallPlan 是组安装计划里要先说的成员:untracked 有不是库装的同名文件夹(合成一次询问，跳过或覆盖),
// blocked 被另一个技能占用或组里同名，装不上。
export function groupInstallPlan(members: InstallPlan[]) {
  return {
    untracked: members.filter((m) => m.status === InstallPlanStatus.InstallUntracked),
    blocked: members.filter((m) => m.status === InstallPlanStatus.InstallOccupied || m.status === InstallPlanStatus.InstallDuplicate),
  };
}

// groupInstalled 是组安装之后：placed 是装上(或已经在那里)的各处，装好后的提示按它看提到的技能找不找得到;
// listed 是要不要逐个列出结果：有被跳过的成员，或计划里就有装不上的。
export function groupInstalled(results: GroupMemberResult[], blocked: InstallPlan[]) {
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  return {
    placed: placedBy(results),
    listed: skipped.length > 0 || blocked.length > 0,
  };
}

// ---------- 卸载 ----------

// uninstallPlace 是从 scope 卸载一个技能会动到的:place 是这一处;shared 是全局共用目录里还读着它的别的 Agent(卸载会撤销它们的直接安装，
// 文件夹随之删除;项目里共用目录的几个 Agent 在界面上本来就是一处，不算);deleted 是会删掉文件夹的各处：直接安装、没有组安装留着时才删。
export function uninstallPlace(s: Snapshot, id: SkillID, scope: Scope) {
  const place = skillScopeIn(s, id, scopeKey(scope));
  return {
    place,
    shared: place ? sharedWithNote(place) : [],
    deleted: place?.direct && !place.groups?.length ? folderPlaces(s, [place]) : [],
  };
}

// asksBeforeUninstall 是卸载一处之前要不要先确认：平时一点就卸载;有要说的才问：全局共用、别处的符号链接指着要删的文件夹、
// 要删的文件夹被手动改过，或别的技能会因此找不到它提到的技能(unfound)。
export function asksBeforeUninstall(p: ReturnType<typeof uninstallPlace>, unfound: MissingMention[]): boolean {
  if (!p.place) return false;
  return p.shared.length > 0 || detachedBy(p.deleted).length > 0 || lostEdits(p.deleted).length > 0 || unfound.length > 0;
}

// uninstallPicked 是从 scopes 卸载勾选的技能会动到的:direct 是要撤销的直接安装，viaGroup 是只随组安装、跳过的;
// deleted 是会删掉文件夹的各处(还有组安装留着的只撤直接安装，文件夹不删),folders 是其中的文件夹数;
// shared 是会删掉的全局共用文件夹(还有组安装留着的不删，各 Agent 照样算已装,不说);skills 是要卸载的技能数。
export function uninstallPicked(s: Snapshot, ids: SkillID[], scopes: Scope[]) {
  const places = scopes.flatMap((sc) => ids.map((id) => skillScopeIn(s, id, scopeKey(sc))).filter((p): p is SkillScopeInspection => !!p));
  const direct = places.filter((p) => p.direct);
  const deleted = folderPlaces(s, direct.filter((p) => !p.groups?.length));
  return {
    direct,
    viaGroup: places.filter((p) => !p.direct),
    deleted,
    folders: new Set(deleted.map(folderKey)).size,
    shared: direct.filter((p) => sharedWithNote(p).length && !p.groups?.length),
    skills: new Set(direct.map((p) => p.skill_id)).size,
  };
}

// uninstalledPicked 归并逐处卸载的结果:errors 与 picked.direct 一一对应，卸成的是 null、没做到的是 undefined。
// 只随组安装的跳过并写明;全局共用一个文件夹的几处，卸载其中一处时其余几处的直接安装已一起撤了(NotFound),不算失败。
// done 是卸载了几处，failed 是有卸不掉的(顶栏画叉)。
export function uninstalledPicked(s: Snapshot, picked: ReturnType<typeof uninstallPicked>, errors: (LibraryError | null | undefined)[]) {
  const t = messages().actions;
  const label = (p: SkillScopeInspection) => `${skillName(s, p.skill_id)} · ${scopeLabel(s, p.scope)}`;
  const rows: ResultRow[] = picked.viaGroup.map((p) => ({ label: label(p), outcome: t.memberSkipped, tone: "warn", reason: t.onlyViaGroup }));
  let done = 0;
  let failed = false;
  picked.direct.forEach((p, i) => {
    const err = errors[i];
    if (err === undefined) return;
    if (err === null) {
      done++;
      return;
    }
    if (err.code === ErrorCode.NotFound) return;
    rows.push({ label: label(p), outcome: t.failed, tone: "critical", reason: describeError(err) });
    failed = true;
  });
  return { rows, done, failed };
}

// uninstallGroupPlaces 是从 scope 卸载整组的组安装会删掉文件夹的成员(gone):只靠这个组安装留着的(没有直接安装，
// 读这个文件夹的各个 Agent(含全局共用目录的)在同一处也没有别的组安装含它);shared 是其中全局共用的，共用的几个 Agent 都不再算已装。
export function uninstallGroupPlaces(s: Snapshot, groupID: GroupID, scope: Scope) {
  const memberOf = (id: GroupID, skill: SkillID) => !!s.groups.find((g) => g.id === id)?.members?.includes(skill);
  const gone = (s.groups.find((g) => g.id === groupID)?.members ?? [])
    .map((id) => skillScopeIn(s, id, scopeKey(scope)))
    .filter((p): p is SkillScopeInspection => !!p && !p.direct)
    .filter((p) => {
      const readers = [scope.agent, ...(p.shared_with ?? [])];
      const others = s.groupInstalls.filter(
        (g) =>
          readers.includes(g.scope.agent) &&
          placeOf(g.scope) === placeOf(scope) &&
          !(g.group_id === groupID && g.scope.agent === scope.agent),
      );
      return !others.some((g) => memberOf(g.group_id, p.skill_id));
    });
  return { gone, shared: gone.filter((p) => sharedWithNote(p).length) };
}
