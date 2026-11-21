// 照着装(copy.tsx)里的判断：确认框里各项的勾选键、一处标题上的勾选、要另外说出的改过的与新版的技能，
// 确认后按勾选逐处要做的事和结果怎么归并。默认勾哪些、覆盖哪些见 lib/consent.ts。不碰界面、不调技能库，单元测试见 copy-rules.test.ts。
import {
  GroupMemberOutcome,
  type Group,
  type GroupMemberResult,
  type Scope,
  type Skill,
  type SkillID,
  type SkillInScope,
} from "@bindings/internal/library";
import { copyable, copyDefaults, copyOverwrites, type CopyPlanned } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { errorOccupier, scopeKey, scopeLabel, type CopyPlace } from "@/lib/model";
import { memberRows, type ResultRow } from "@/components/app/result-rows";
import { occupied } from "./install-rules";

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

// CopyOutcome 是一步执行的结果：组安装是逐个成员的结果，直接安装成功时为空;失败时是技能库错误。
export type CopyOutcome = { results?: GroupMemberResult[] } | { error: LibraryError };

// copyResults 归并照着装的结果:outcomes 与 steps 一一对应，跳过的和没做到的是 undefined。
// installed 按"哪一处的哪个技能"算，同一个技能既随组又直接安装时只算一次;groups 是记下的组安装数;placed 是装上的各处。
// 组安装里有不是库装的同名文件夹而没勾覆盖、被跳过的成员是用户自己选的跳过，不再列出;其余被跳过的成员照样列出原因。
// 直接安装执行时才被同一批里先装的同名技能占上的，错误里有占着的那个，照样能点开。failed 是有失败的(顶栏画叉)。
export function copyResults(s: Snapshot, steps: CopyStep[], outcomes: (CopyOutcome | undefined)[]) {
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
    const results = o.results ?? [];
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
