// 写操作结果列表里的一行(components/app/results.tsx 的 showResults 列出它们):组安装、加入组的逐个成员，重新安装失败的条目。
// 不碰界面、不调技能库，流程里的判断(features/ 下的 *-rules.ts)也用它们归并结果。
import { ErrorCode, GroupMemberOutcome, type GroupMemberResult, type ReinstallResult, type SkillID } from "@bindings/internal/library";
import { describeError, occupiedReason } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { Attempt, Snapshot } from "@/lib/library";
import type { Tone } from "@/lib/level";
import { errorOccupier } from "@/lib/installs";
import { shortPath } from "@/lib/paths";
import { scopeLabel } from "@/lib/places";
import { sameNameInLibrary, twinNote } from "@/lib/same-name";
import { skillName } from "@/lib/scope";

// ResultRow 是一条结果;detail 是库里有同名技能时这一行是哪一个(写在名字下面);occupier 是安装时占着文件夹的另一个同名技能，
// 这一行能点开它(先关掉结果)。
export interface ResultRow {
  label: string;
  detail?: string;
  outcome: string;
  tone: Tone;
  reason?: string;
  occupier?: SkillID;
}

// failedAttemptRows 是逐项调用(runEach)里失败的那几项，label 是第 i 项在结果里的名字;outcome 不给时写「失败」。
export function failedAttemptRows(results: readonly (Attempt<unknown> | undefined)[], label: (i: number) => string, outcome = messages().actions.failed): ResultRow[] {
  return results.flatMap((r, i) => (r && "error" in r ? [{ label: label(i), outcome, tone: "critical" as const, reason: describeError(r.error) }] : []));
}

function memberOutcome(outcome: string): { text: string; tone: ResultRow["tone"] } | undefined {
  const t = messages().actions;
  return {
    [GroupMemberOutcome.MemberInstalled]: { text: t.memberInstalled, tone: "plain" as const },
    [GroupMemberOutcome.MemberAlreadyInstalled]: { text: t.memberAlready, tone: "plain" as const },
    [GroupMemberOutcome.MemberSkipped]: { text: t.memberSkipped, tone: "warn" as const },
  }[outcome];
}

// twinDetail 是库里有同名的技能时，这个技能是哪一个(来源、装在哪，见 twinNote):同名的几个一起出现在结果里时靠它分辨;没有同名的为 undefined。
// s 是写之前的快照，results 是这次的逐个结果：这次装上的那几处一起算进装在哪，不说成还没装。
export function twinDetail(s: Snapshot, id: SkillID, results: readonly GroupMemberResult[] = []): string | undefined {
  const skill = s.skills.find((x) => x.id === id);
  const placed = placedBy([...results]).filter((r) => r.skill_id === id).map((r) => r.scope);
  return skill && sameNameInLibrary(s, skill).length ? twinNote(s, skill, placed) : undefined;
}

// memberRows 是组安装、加入组的逐个结果;被同名的另一个技能占用而跳过的，能点开占着的那个(错误里带着它)。
// adding 是把技能加入组(组里已有同名的那一个可能装着),否则是整组安装(同名的几个都没装)。
export function memberRows(s: Snapshot, results: GroupMemberResult[], adding = false): ResultRow[] {
  return results.map((r) => ({
    label: `${skillName(s, r.skill_id)} → ${scopeLabel(s, r.scope)}`,
    detail: twinDetail(s, r.skill_id, results),
    outcome: memberOutcome(r.outcome)?.text ?? r.outcome,
    tone: memberOutcome(r.outcome)?.tone ?? "info",
    reason: memberReason(s, r, adding),
    occupier: errorOccupier(s, r.skill_id, r.error),
  }));
}

// memberReason 说明一个成员为什么跳过，和单个安装时的说法一致(occupiedReason),路径按界面惯例缩写(主目录下的写成 ~);
// 按技能库给的字段认原因(见 GroupMemberResult),认不出的照 describeError 写。
function memberReason(s: Snapshot, r: GroupMemberResult, adding: boolean): string | undefined {
  const err = r.error;
  if (!err) return undefined;
  const t = messages().actions;
  const d = err.details ?? {};
  const path = d.path ? shortPath(s, d.path) : "";
  if (err.code === ErrorCode.AlreadyExists && path) return t.memberUntracked(path);
  if (err.code === ErrorCode.FailedPrecondition && path) {
    if (d.skill_id) return occupiedReason(s, r.skill_id, d.skill_id, d.path!);
    if (d.name) return adding ? t.memberDuplicateAdded(d.name, path) : t.memberDuplicate(d.name, path);
    if (d.project_id) return t.memberRootMissing(path);
  }
  return describeError(err);
}

// placedBy 是组安装、加进组时随组安装装上(或已经在那里)的各处，装好后的提示按它看提到的技能找不找得到。
export function placedBy(results: GroupMemberResult[]): GroupMemberResult[] {
  return results.filter((r) => r.outcome !== GroupMemberOutcome.MemberSkipped);
}

export function summarizeMembers(results: GroupMemberResult[]): string {
  const count = (o: GroupMemberOutcome) => results.filter((r) => r.outcome === o).length;
  return messages().actions.membersSummary(
    count(GroupMemberOutcome.MemberInstalled),
    count(GroupMemberOutcome.MemberAlreadyInstalled),
    count(GroupMemberOutcome.MemberSkipped),
  );
}

// failedRow 只用于重新安装失败的条目;成功的只汇总计数。
export function failedRow(s: Snapshot, r: ReinstallResult): ResultRow {
  return {
    label: `${skillName(s, r.skill_id)} · ${scopeLabel(s, r.scope)}`,
    outcome: messages().actions.failed,
    tone: "critical",
    reason: r.error ? describeError(r.error) : undefined,
  };
}

