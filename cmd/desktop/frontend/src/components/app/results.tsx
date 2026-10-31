// 写操作的结果列表：组安装、加入组、重新安装等逐条返回结果时，列出每一条的结论，被跳过或失败的写明原因。
import { GroupMemberOutcome, type GroupMemberResult, type ReinstallResult } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { toLibraryError } from "@/lib/library";
import { messages } from "@/lib/i18n";
import type { Snapshot } from "@/lib/library";
import { scopeLabel, skillName } from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";

export interface ResultRow {
  label: string;
  outcome: string;
  tone: "ok" | "warn" | "bad";
  reason?: string;
}

export function showResults(title: string, rows: ResultRow[], note?: string) {
  const t = messages().actions;
  return ui.open((close) => (
    <Modal
      title={title}
      onClose={() => close()}
      footer={
        <Button variant="primary" onClick={() => close()}>
          {t.gotIt}
        </Button>
      }
    >
      {note && <Notice>{note}</Notice>}
      <ListBox>
        {rows.map((r, i) => (
          <ListRow key={i}>
            <div className="min-w-0 grow">
              <div>{r.label}</div>
              {r.reason && <div className="text-xs whitespace-pre-line text-muted-foreground">{r.reason}</div>}
            </div>
            <Badge tone={r.tone}>{r.outcome}</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  ));
}

function memberOutcome(outcome: string): { text: string; tone: ResultRow["tone"] } | undefined {
  const t = messages().actions;
  return {
    [GroupMemberOutcome.MemberInstalled]: { text: t.memberInstalled, tone: "ok" as const },
    [GroupMemberOutcome.MemberAlreadyInstalled]: { text: t.memberAlready, tone: "ok" as const },
    [GroupMemberOutcome.MemberSkipped]: { text: t.memberSkipped, tone: "warn" as const },
  }[outcome];
}

export function memberRows(s: Snapshot, results: GroupMemberResult[]): ResultRow[] {
  return results.map((r) => ({
    label: `${skillName(s, r.skill_id)} → ${scopeLabel(s, r.scope)}`,
    outcome: memberOutcome(r.outcome)?.text ?? r.outcome,
    tone: memberOutcome(r.outcome)?.tone ?? "warn",
    reason: r.error ? describeError(r.error) : undefined,
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


// runEach 逐项做一次写操作(批量移除、批量移出组等):一个失败不影响其余的，返回失败的那几项的结果行;
// 有失败时顶栏的状态圈画叉。label 是一项在结果里的名字。
export async function runEach<T>(items: T[], call: (item: T) => Promise<unknown>, label: (item: T) => string): Promise<ResultRow[]> {
  const t = messages().actions;
  const rows: ResultRow[] = [];
  await ui.run(
    async () => {
      for (const item of items) {
        try {
          await call(item);
        } catch (e) {
          rows.push({ label: label(item), outcome: t.failed, tone: "bad", reason: describeError(toLibraryError(e)) });
        }
      }
    },
    { failed: () => rows.length > 0 },
  );
  return rows;
}

// failedRow 只用于重新安装失败的条目;成功的只汇总计数。
export function failedRow(s: Snapshot, r: ReinstallResult): ResultRow {
  return {
    label: `${skillName(s, r.skill_id)} · ${scopeLabel(s, r.scope)}`,
    outcome: messages().actions.failed,
    tone: "bad",
    reason: r.error ? describeError(r.error) : undefined,
  };
}

