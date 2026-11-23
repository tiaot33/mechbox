// 组(group.tsx)里的判断：勾选的技能里哪些要加入、哪些要移出，删组、移出组时选「一并卸载」会删掉哪些文件夹，
// 逐个加入组之后是只提示一句还是逐个列出结果。不碰界面、不调技能库，单元测试见 group-rules.test.ts。
import {
  GroupMemberOutcome,
  InstallPlanStatus,
  type CollectionID,
  type GroupID,
  type GroupMemberResult,
  type InstallPlan,
  type Scope,
  type SkillID,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { Attempt, Snapshot } from "@/lib/library";
import { scopeKey, skillName } from "@/lib/scope";
import { memberRows, placedBy, type ResultRow } from "@/components/app/result-rows";
import { readsTwiceNotes } from "@/features/install/install-rules";
import { scopeLabel } from "@/lib/places";

// notInGroup 是勾选的技能里还不在组里的(要加入的),inGroup 是在组里的(要移出的)。
export function notInGroup(s: Snapshot, groupID: GroupID, picked: SkillID[]): SkillID[] {
  const members = s.groups.find((g) => g.id === groupID)?.members ?? [];
  return picked.filter((id) => !members.includes(id));
}

export function inGroup(s: Snapshot, groupID: GroupID, picked: SkillID[]): SkillID[] {
  return picked.filter((id) => s.groups.find((g) => g.id === groupID)?.members?.includes(id));
}

// groupOnly 是这个组在它装到的各作用域里，只靠它留着的成员(没有直接安装、也没有别的组安装):选了一并卸载时这些文件夹会删。
export function groupOnly(s: Snapshot, groupID: GroupID, members: SkillID[]): SkillScopeInspection[] {
  const scopes = new Set(s.groupInstalls.filter((g) => g.group_id === groupID).map((g) => scopeKey(g.scope)));
  return s.skillScopes.filter(
    (p) => members.includes(p.skill_id) && scopes.has(scopeKey(p.scope)) && !p.direct && (p.groups ?? []).every((g) => g === groupID),
  );
}

// addedToGroup 归并逐个加入组的结果:results 与 ids 一一对应(runEach 的结果),加成的是随组安装到各作用域的逐个结果。
// all 是都加入了、也没有作用域被跳过，只提示一句;
// 否则逐个列出 rows:失败的写原因，有组安装的列出各作用域的结果，没有组安装的写已加入。skipped 是有作用域被跳过，
// added 是加入了几个，placed 是随组安装装上的各处。
export function addedToGroup(s: Snapshot, ids: SkillID[], results: readonly (Attempt<GroupMemberResult[]> | undefined)[]) {
  const t = messages().actions;
  const added = results.flatMap((r) => (r && "value" in r ? [r.value] : []));
  const skipped = added.some((members) => members.some((r) => r.outcome === GroupMemberOutcome.MemberSkipped));
  const rows = ids.flatMap((id, i): ResultRow[] => {
    const r = results[i];
    if (!r) return [];
    if ("error" in r) return [{ label: skillName(s, id), outcome: t.failed, tone: "critical", reason: describeError(r.error) }];
    if (r.value.length) return memberRows(s, r.value, true);
    return [{ label: skillName(s, id), outcome: t.memberAdded, tone: "plain" }];
  });
  return { all: added.length === ids.length && !skipped, skipped, added: added.length, placed: added.flatMap(placedBy), rows };
}

// missingFromGroup 是集合里还不在组里的技能：集合拖到组上、右键"加入组"时加入的就是这些;为空时说"都已在组里"。
export function missingFromGroup(s: Snapshot, collectionID: CollectionID, groupID: GroupID): SkillID[] {
  const members = new Set(s.groups.find((g) => g.id === groupID)?.members ?? []);
  return (s.collections.find((c) => c.id === collectionID)?.skills ?? []).filter((id) => !members.has(id));
}

// addReadsTwice 是把技能加入组之前要提示的「会读到两份」(#378:加入组时照装，事先提示):对这个组已组安装的各处取了安装计划，
// 会装上的(没被占用)里有别的 Agent 也读这个目录、又在自己别的目录装着这个技能的，一处一行，写出是哪一处。
export function addReadsTwice(s: Snapshot, plans: readonly { scope: Scope; plan: InstallPlan }[]): string[] {
  const installs = plans.filter(({ plan }) => plan.status === InstallPlanStatus.InstallReady || plan.status === InstallPlanStatus.InstallUntracked);
  return readsTwiceNotes(
    s,
    installs.map(({ scope, plan }) => ({ plan, label: scopeLabel(s, scope) })),
  );
}
