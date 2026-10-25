// 检查更新：按来源读一次，只写下检查结论，不改技能也不碰装出去的文件夹(ADR 0010);读不成的来源列出原因。
import { LibraryService } from "@bindings/cmd/desktop";
import type { CollectionID } from "@bindings/internal/library";
import { create } from "zustand";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { deletedInSource, pending, renamedTo, sourceLabel, updateAvailable } from "@/lib/model";
import { ui } from "@/lib/ui";
import { showResults } from "@/components/app/results";
import type { MarkStatus } from "@/components/ui/status-mark";

// 左栏"检查更新"按钮上的状态圈：检查中转圈，结束后画出对勾(有来源没读成或调用失败的画叉),停一下换回原来的图标。
// 从集合右键菜单发起的检查也画在这里;顶栏的状态圈不跟着转。
const useCheckMark = create<{ status: MarkStatus | null }>()(() => ({ status: null }));
let markTimer = 0;

export function useCheckStatus(): MarkStatus | null {
  return useCheckMark((s) => s.status);
}

// checkUpdates 按集合检查更新(只读);没读成来源的集合列出原因。汇报的数目只算计入角标的(跳过的、选了继续保留的不算)。
export async function checkUpdates(collections: CollectionID[]) {
  const s = currentSnapshot();
  const t = messages().actions;
  window.clearTimeout(markTimer);
  useCheckMark.setState({ status: "running" });
  const checks = await ui.run(() => LibraryService.CheckUpdates(collections), {
    failed: (checks) => (checks ?? []).some((c) => c.error),
    mark: false,
  });
  const failed = (checks ?? []).filter((c) => c.error);
  useCheckMark.setState({ status: !checks || failed.length ? "failed" : "done" });
  markTimer = window.setTimeout(() => useCheckMark.setState({ status: null }), 1100);
  if (!checks) return;
  const skills = checks.flatMap((c) => c.skills ?? []).filter(pending);
  const available = skills.filter(updateAvailable).length;
  const renamed = skills.filter(renamedTo).length;
  const deleted = skills.filter(deletedInSource).length;
  const summary = t.checkSummary(checks.length, available, renamed, deleted);
  if (failed.length) {
    await showResults(
      t.checkUpdates,
      failed.map((c) => ({
        label: c.source?.label ? sourceLabel(s, c.source) : t.collectionFallback(c.collection_id),
        outcome: t.checkFailed,
        tone: "bad" as const,
        reason: describeError(c.error!),
      })),
      t.checkFailedNote(summary, failed.length),
    );
  } else {
    ui.toast(skills.length ? summary : t.nothingPending(checks.length));
  }
}
