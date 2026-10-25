// 检查更新：按来源读一次，只写下检查结论，不改技能也不碰装出去的文件夹(ADR 0010);读不成的来源列出原因。
import { LibraryService } from "@bindings/cmd/desktop";
import type { CollectionID } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { deletedInSource, pending, renamedTo, sourceLabel, updateAvailable } from "@/lib/model";
import { ui } from "@/lib/ui";
import { showResults } from "@/components/app/results";

// checkUpdates 按集合检查更新(只读);没读成来源的集合列出原因。汇报的数目只算计入角标的(跳过的、选了继续保留的不算)。
export async function checkUpdates(collections: CollectionID[]) {
  const s = currentSnapshot();
  const t = messages().actions;
  const checks = await ui.run(() => LibraryService.CheckUpdates(collections), { failed: (checks) => (checks ?? []).some((c) => c.error) });
  if (!checks) return;
  const skills = checks.flatMap((c) => c.skills ?? []).filter(pending);
  const available = skills.filter(updateAvailable).length;
  const renamed = skills.filter(renamedTo).length;
  const deleted = skills.filter(deletedInSource).length;
  const summary = t.checkSummary(checks.length, available, renamed, deleted);
  const failed = checks.filter((c) => c.error);
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
