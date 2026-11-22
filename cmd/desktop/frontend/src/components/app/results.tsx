// 写操作的结果列表：组安装、加入组、重新安装等逐条返回结果时，列出每一条的结论，被跳过或失败的写明原因。
// 结果行怎么写(ResultRow、memberRows 等)在 result-rows.ts,是不碰界面的纯函数，流程里的判断也用它们归并结果。
import { ErrorCode } from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import { toLibraryError, type Attempt } from "@/lib/library";
import { Modal, ui } from "@/lib/ui";
import { openDrawer } from "@/lib/view";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import type { ResultRow } from "./result-rows";

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

// eachCall 逐项调用技能库：一项失败不影响其余的，返回与 items 一一对应的结果;call 返回 undefined 的那一项跳过，结果里是 undefined。
// 某一项被取消时不再做后面的，把取消抛给外面的 run,整批和被取消的调用一样算没做成。要和别的调用算成一次写操作时用它，否则用 runEach。
export async function eachCall<T, R>(items: readonly T[], call: (item: T) => Promise<R> | undefined): Promise<(Attempt<R> | undefined)[]> {
  const results: (Attempt<R> | undefined)[] = [];
  for (const item of items) {
    const pending = call(item);
    if (!pending) {
      results.push(undefined);
      continue;
    }
    try {
      results.push({ value: await pending });
    } catch (e) {
      const error = toLibraryError(e);
      if (error.code === ErrorCode.Canceled) throw e;
      results.push({ error });
    }
  }
  return results;
}

// runEach 是一次写操作里的 eachCall(批量安装、卸载、照着装、批量移除等);被取消时返回 undefined。
// failed 由结果判断顶栏的状态圈画不画叉，不给时有一项失败就画叉。
export function runEach<T, R>(
  items: readonly T[],
  call: (item: T) => Promise<R> | undefined,
  failed: (results: (Attempt<R> | undefined)[]) => boolean = (results) => results.some((r) => r && "error" in r),
): Promise<(Attempt<R> | undefined)[] | undefined> {
  return ui.run(() => eachCall(items, call), { failed });
}
