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
import { useMarkStatus, type MarkStatus, type Settled } from "@/components/ui/status-mark";

// CheckOrigin 是检查从哪里发起:"all" 是左栏的检查更新按钮，否则是右键的那个集合。进度画在发起处，顶栏的状态圈不跟着转;
// 左栏不显示时(收起、在设置页)由顶栏的状态圈补上。
export type CheckOrigin = "all" | CollectionID;

// 进行中的检查与最近一次结束的结果，各带发起处。
const useCheckStore = create<{ running: CheckOrigin | null; settled: (Settled & { origin: CheckOrigin }) | null }>()(() => ({
  running: null,
  settled: null,
}));
let nextSettledID = 1;

// useCheckMark 是检查的状态圈该画的状态：只看从 origin 发起的检查，不给时看所有检查;没有要画的时是 null。
export function useCheckMark(origin?: CheckOrigin): MarkStatus | null {
  const running = useCheckStore((s) => s.running !== null && (origin === undefined || s.running === origin));
  const settled = useCheckStore((s) => (s.settled && (origin === undefined || s.settled.origin === origin) ? s.settled : null));
  return useMarkStatus(running, settled);
}

// checkUpdates 按集合检查更新(只读);没读成来源的集合列出原因。汇报的数目只算计入角标的(跳过的、选了继续保留的不算)。
export async function checkUpdates(collections: CollectionID[], origin: CheckOrigin = "all") {
  const s = currentSnapshot();
  const t = messages().actions;
  useCheckStore.setState({ running: origin });
  const checks = await ui.run(() => LibraryService.CheckUpdates(collections), { busyMark: false });
  const failed = (checks ?? []).filter((c) => c.error);
  useCheckStore.setState({ running: null, settled: { id: nextSettledID++, failed: !checks || failed.length > 0, origin } });
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
