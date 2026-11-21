// 写操作的结果列表：组安装、加入组、重新安装等逐条返回结果时，列出每一条的结论，被跳过或失败的写明原因。
// 结果行怎么写(ResultRow、memberRows 等)在 result-rows.ts,是不碰界面的纯函数，流程里的判断也用它们归并结果。
import { describeError } from "@/lib/errors";
import { toLibraryError } from "@/lib/library";
import { messages } from "@/lib/i18n";
import { Modal, ui } from "@/lib/ui";
import { openDrawer } from "@/lib/view";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import type { ResultRow } from "./result-rows";

export { failedRow, memberRows, summarizeMembers, type ResultRow } from "./result-rows";

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
              {r.reason && <div className="text-body whitespace-pre-line text-muted-foreground">{r.reason}</div>}
              {r.occupier && (
                <Button
                  variant="link"
                  size="inline"
                  className="open-occupier"
                  onClick={() => {
                    close();
                    openDrawer(r.occupier!);
                  }}
                >
                  {t.openOccupier}
                </Button>
              )}
            </div>
            <Badge tone={r.tone}>{r.outcome}</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  ));
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
          rows.push({ label: label(item), outcome: t.failed, tone: "critical", reason: describeError(toLibraryError(e)) });
        }
      }
    },
    { failed: () => rows.length > 0 },
  );
  return rows;
}
