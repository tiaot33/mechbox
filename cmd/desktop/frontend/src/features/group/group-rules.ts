// 组(group.tsx)里的判断：勾选的技能里哪些要加入、哪些要移出，删组、移出组时选「一并卸载」会删掉哪些文件夹，
// 逐个加入组之后是只提示一句还是逐个列出结果。不碰界面、不调技能库，单元测试见 group-rules.test.ts。
import { GroupMemberOutcome, type GroupID, type GroupMemberResult, type SkillID, type SkillScopeInspection } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { scopeKey, skillName } from "@/lib/model";
import { memberRows, placedBy, type ResultRow } from "@/components/app/result-rows";

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

// AddedToGroup 是把一个技能加入组的结果:results 是随组安装到各作用域的逐个结果;没加成时是 error。
export type AddedToGroup = { id: SkillID; results: GroupMemberResult[]; error?: LibraryError };

// addedToGroup 归并逐个加入组的结果(outcomes 按 ids 的先后):all 是都加入了、也没有作用域被跳过，只提示一句;
// 否则逐个列出 rows:失败的写原因，有组安装的列出各作用域的结果，没有组安装的写已加入。skipped 是有作用域被跳过，
// added 是加入了几个，placed 是随组安装装上的各处。
export function addedToGroup(s: Snapshot, ids: SkillID[], outcomes: AddedToGroup[]) {
  const t = messages().actions;
  const added = outcomes.filter((o) => !o.error);
  const skipped = added.some((o) => o.results.some((r) => r.outcome === GroupMemberOutcome.MemberSkipped));
  const rows = outcomes.flatMap((o): ResultRow[] => {
    if (o.error) return [{ label: skillName(s, o.id), outcome: t.failed, tone: "critical", reason: describeError(o.error) }];
    if (o.results.length) return memberRows(s, o.results);
    return [{ label: skillName(s, o.id), outcome: t.memberAdded, tone: "plain" }];
  });
  return { all: added.length === ids.length && !skipped, skipped, added: added.length, placed: added.flatMap((o) => placedBy(o.results)), rows };
}
