// 写操作结果列表里的一行(results.tsx 的 showResults 列出它们):组安装、加入组的逐个成员，重新安装失败的条目。
// 不碰界面、不调技能库，流程里的判断(features/ 下的 *-rules.ts)也用它们归并结果。
import { ErrorCode, GroupMemberOutcome, type GroupMemberResult, type ReinstallResult, type SkillID } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { Attempt, Snapshot } from "@/lib/library";
import type { Tone } from "@/lib/level";
import { errorOccupier } from "@/lib/installs";
import { shortPath } from "@/lib/paths";
import { scopeLabel } from "@/lib/places";
import { noSource, sourceLabel } from "@/lib/source";
import { skillName } from "@/lib/scope";

// ResultRow 是一条结果;occupier 是安装时占着文件夹的另一个同名技能，这一行能点开它(先关掉结果)。
export interface ResultRow {
  label: string;
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

// memberRows 是组安装、加入组的逐个结果;被同名的另一个技能占用而跳过的，能点开占着的那个(错误里带着它)。
export function memberRows(s: Snapshot, results: GroupMemberResult[]): ResultRow[] {
  return results.map((r) => ({
    label: `${skillName(s, r.skill_id)} → ${scopeLabel(s, r.scope)}`,
    outcome: memberOutcome(r.outcome)?.text ?? r.outcome,
    tone: memberOutcome(r.outcome)?.tone ?? "info",
    reason: memberReason(s, r),
    occupier: errorOccupier(s, r.skill_id, r.error),
  }));
}

// memberReason 说明一个成员为什么跳过，和单个安装时的说法一致，路径按界面惯例缩写(主目录下的写成 ~);
// 认不出的原因照 describeError 写。
function memberReason(s: Snapshot, r: GroupMemberResult): string | undefined {
  const err = r.error;
  if (!err) return undefined;
  const t = messages().actions;
  const d = err.details ?? {};
  const path = d.path ? shortPath(s, d.path) : "";
  if (err.code === ErrorCode.AlreadyExists && path) return t.memberUntracked(path);
  if (err.code === ErrorCode.FailedPrecondition && path) {
    if (d.skill_id === r.skill_id) return t.occupiedSelf(path);
    if (d.skill_id) {
      const occupier = s.skills.find((x) => x.id === d.skill_id);
      return !occupier ? t.occupiedOther(path) : noSource(occupier.source) ? t.occupiedNoSource(path) : t.occupiedSource(sourceLabel(s, occupier.source), path);
    }
    return d.name ? t.memberDuplicate(d.name, path) : t.memberRootMissing(path);
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

