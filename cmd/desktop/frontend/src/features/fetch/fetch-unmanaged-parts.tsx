// 一次确认入库未纳管技能时用到的辅助：几个小标记、入库后的提示，以及按来源分批入库(来源的分组键、最终用的来源、入库请求见 fetch-unmanaged-rules.ts)。
import type { FetchedUnmanagedSkill, PlannedUnmanagedSkill } from "@bindings/internal/library";
import { LibraryService } from "@bindings/cmd/desktop";
import { messages, useT } from "@/lib/i18n";
import { type LibraryError, toLibraryError } from "@/lib/library";
import { homePath } from "@/lib/paths";
import { sameNameFetchNote } from "@/lib/same-name";
import { Badge } from "@/components/ui/badge";
import { fetchBatches, itemOf, keyOf, outsideTarget, sourceOf } from "./fetch-unmanaged-rules";

export function OutsideBadge({ planned, home }: { planned: PlannedUnmanagedSkill; home: string }) {
  const t = useT().unmanaged;
  return (
    <Badge tone="info" title={t.outsideTitle(homePath(home, outsideTarget(planned)))}>
      {t.outside}
    </Badge>
  );
}

// Failed 是没入库的一个技能：留着原来的那一项，用户可以改来源链接后重试。
export interface Failed {
  key: string;
  planned: PlannedUnmanagedSkill;
  error: LibraryError;
  link: string;
  busy: boolean;
}

export function SameNameInLibrary({ name, sourced }: { name: string; sourced: boolean }) {
  const t = useT().unmanaged;
  return (
    <Badge className="fetch-unmanaged-same-name" title={t.sameNameTitle(name, sameNameFetchNote(name, sourced))}>
      {t.sameName}
    </Badge>
  );
}

export function fetchedMessage(done: FetchedUnmanagedSkill[]): string {
  return messages().unmanaged.fetched(done.length, done.filter((r) => r.matched).length);
}

// fetchInBatches 把选中的几条入库：按最终用的来源分批，同一个来源只读一次;onBatch 在读每一批之前告诉调用方在等哪个来源
// (sourceKey,没有来源的一批在最后)。一批里失败的、整批调用失败的都记进 lost,留着原来的那一项和来源链接，好让用户改了重试。
export async function fetchInBatches(
  items: { planned: PlannedUnmanagedSkill; link?: string }[],
  onBatch: (key: string) => void,
): Promise<{ done: FetchedUnmanagedSkill[]; lost: Failed[] }> {
  const done: FetchedUnmanagedSkill[] = [];
  const lost: Failed[] = [];
  const fail = ({ planned, link }: (typeof items)[number], error: LibraryError) =>
    lost.push({ key: keyOf(planned), planned, error, link: sourceOf(planned, link).input ?? "", busy: false });
  for (const [key, members] of fetchBatches(items)) {
    onBatch(key);
    try {
      const results = (await LibraryService.FetchUnmanagedSkills({ skills: members.map((m) => itemOf(m.planned, m.link)) })) ?? [];
      results.forEach((r, n) => (r.error ? fail(members[n], r.error) : done.push(r)));
    } catch (err) {
      const error = toLibraryError(err);
      for (const m of members) fail(m, error);
    }
  }
  return { done, lost };
}
