// 写操作结果列表里的一行(results.tsx 的 showResults 列出它们):组安装、加入组的逐个成员，重新安装失败的条目。
// 不碰界面、不调技能库，流程里的判断(features/ 下的 *-rules.ts)也用它们归并结果。
import { GroupMemberOutcome, type GroupMemberResult, type ReinstallResult, type SkillID } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { Snapshot } from "@/lib/library";
import type { Tone } from "@/lib/level";
import { errorOccupier, scopeLabel, skillName } from "@/lib/model";

// ResultRow 是一条结果;occupier 是安装时占着文件夹的另一个同名技能，这一行能点开它(先关掉结果)。
export interface ResultRow {
  label: string;
  outcome: string;
  tone: Tone;
  reason?: string;
  occupier?: SkillID;
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
    reason: r.error ? describeError(r.error) : undefined,
    occupier: errorOccupier(s, r.skill_id, r.error),
  }));
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

