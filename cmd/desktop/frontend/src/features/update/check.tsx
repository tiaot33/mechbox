// 检查更新：按来源读一次，只写下检查结论，不改技能也不碰装出去的文件夹(ADR 0010);读不成的来源列出原因。
import { LibraryService } from "@bindings/cmd/desktop";
import type { CollectionID } from "@bindings/internal/library";
import { create } from "zustand";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import { currentSnapshot, queryClient, reloadLibrary, snapshotQuery, type Snapshot } from "@/lib/library";
import { isLocal, sourceLabel } from "@/lib/source";
import { sourceChanges } from "@/lib/source-state";
import { ui } from "@/lib/ui";
import { showResults } from "@/components/app/results";
import { useMarkStatus, type MarkStatus } from "@/components/ui/status-mark";
import type { Settled } from "@/lib/settled";

// CheckOrigin 是检查从哪里发起:"all" 是顶栏的检查更新按钮，否则是左栏右键的那个集合。进度画在发起处，顶栏标题旁的状态圈不跟着转;
// 从集合发起、左栏又不显示时(收起、在设置页),由顶栏标题旁的状态圈补上。
export type CheckOrigin = "all" | CollectionID;

// 进行中的检查与最近一次结束的结果，各带发起处。
const useCheckStore = create<{ running: CheckOrigin | null; settled: (Settled & { origin: CheckOrigin }) | null }>()(() => ({
  running: null,
  settled: null,
}));
let nextSettledID = 1;

// useCheckMark 是检查的状态圈该画的状态：只看从 origin 发起的检查,"collections" 看从任一集合发起的，不给时看所有检查;没有要画的时是 null。
export function useCheckMark(origin?: CheckOrigin | "collections"): MarkStatus | null {
  const from = (o: CheckOrigin) => origin === undefined || (origin === "collections" ? o !== "all" : o === origin);
  const running = useCheckStore((s) => s.running !== null && from(s.running));
  const settled = useCheckStore((s) => (s.settled && from(s.settled.origin) ? s.settled : null));
  return useMarkStatus(running, settled);
}

// checkUpdates 按集合检查更新(只读);没读成来源的集合列出原因。汇报的数目只算计入角标的(跳过的、选了继续保留的不算)。
export async function checkUpdates(collections: CollectionID[], origin: CheckOrigin = "all") {
  const s = currentSnapshot();
  const t = messages().actions;
  useCheckStore.setState({ running: origin });
  const checks = await ui.run(() => LibraryService.CheckUpdates(collections), { busyMark: false });
  const failed = (checks ?? []).filter((c) => c.error);
  useCheckStore.setState({ running: null, settled: { id: nextSettledID++, failed: !checks || failed.length > 0, at: Date.now(), origin } });
  if (!checks) return;
  const found = sourceChanges(checks.flatMap((c) => c.skills ?? []));
  const summary = t.checkSummary(checks.length, found.newVersion.length, found.renamed.length, found.deleted.length);
  if (failed.length) {
    await showResults(
      t.checkUpdates,
      failed.map((c) => ({
        label: c.source?.label ? sourceLabel(s, c.source) : t.collectionFallback(c.collection_id),
        outcome: t.checkFailed,
        tone: "critical" as const,
        reason: describeError(c.error!),
      })),
      t.checkFailedNote(summary, failed.length),
    );
  } else {
    ui.toast(found.todo.length ? summary : t.nothingPending(checks.length));
  }
}

// checkLocalDirectories 读一遍本地目录来源的目录，比出哪些技能在目录里改过(结论写在技能的 LastUpdateCheck 上，列表上标「目录里改过」)。
// 只读：不改技能，也不碰装出去的文件夹;打开 app、窗口切回前台时在后台做，不弹提示、不转状态圈，读不成就算了。
// 同时只做一次，免得频繁切窗口时叠起来。
let localChecking = false;
export async function checkLocalDirectories() {
  if (localChecking) return;
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  const ids = s?.collections.filter((c) => isLocal(c.source)).map((c) => c.id) ?? [];
  if (!ids.length) return;
  localChecking = true;
  try {
    // 结论写在技能上，目录里新加、去掉的技能写在集合上(新技能):读完总要重读一次快照。
    await LibraryService.CheckUpdates(ids);
    await reloadLibrary();
  } catch {
    // 目录读不成(移走了、没权限)时列表上照旧，用户点「重新读取」时再说原因。
  } finally {
    localChecking = false;
  }
}

// checkableCollections 是检查更新的范围：本地目录来源不检查。
export function checkableCollections(s: Snapshot): CollectionID[] {
  return s.collections.filter((c) => !isLocal(c.source)).map((c) => c.id);
}
