// 一次确认入库未纳管技能时用到的辅助：来源的分组键、最终用的来源、入库请求，以及几个小标记。
import type { FetchedUnmanagedSkill, PlannedUnmanagedSkill, UnmanagedFetchItem } from "@bindings/internal/library";
import { LibraryService } from "@bindings/cmd/desktop";
import { messages, useT } from "@/lib/i18n";
import { type LibraryError, toLibraryError } from "@/lib/library";
import { homePath, sameNameFetchNote } from "@/lib/model";
import { Badge } from "@/components/ui/badge";

export const noSource = "";

// sourceKey 把来源与分支合成一个键：同一个来源只读一次，结果也按它分组。
export function sourceKey(input?: string, ref?: string): string {
  return input ? `${input}\n${ref ?? ""}` : noSource;
}

export function foldersOf(planned: PlannedUnmanagedSkill) {
  return planned.folders ?? [];
}

// outsideTarget 是指向 Agent 目录以外的那一处链接指向的地方。
export function outsideTarget(planned: PlannedUnmanagedSkill) {
  return foldersOf(planned).find((f) => f.link_outside)?.link_target ?? "";
}

export function OutsideBadge({ planned, home }: { planned: PlannedUnmanagedSkill; home: string }) {
  const t = useT().unmanaged;
  return (
    <Badge tone="warn" title={t.outsideTitle(homePath(home, outsideTarget(planned)))}>
      {t.outside}
    </Badge>
  );
}

// keyOf 是计划里一条的稳定身份：第一处文件夹。
export function keyOf(planned: PlannedUnmanagedSkill): string {
  return foldersOf(planned)[0]?.folder ?? planned.name;
}

// sourceOf 是一条最终用的来源：用户改过链接就用改过的(留空就是没有来源);来源记录里的分支只跟着它认出的那个来源走。
export function sourceOf(
  planned: PlannedUnmanagedSkill,
  link?: string,
): { input?: string; ref?: string } {
  if (link === undefined)
    return { input: planned.source_input, ref: planned.source_ref };
  const input = link.trim();
  return {
    input: input || undefined,
    ref:
      input && input === planned.source_input ? planned.source_ref : undefined,
  };
}

export function itemOf(
  planned: PlannedUnmanagedSkill,
  link?: string,
): UnmanagedFetchItem {
  const { input, ref } = sourceOf(planned, link);
  return {
    folders: foldersOf(planned).map((f) => ({ scope: f.scope, name: f.name })),
    source_input: input,
    source_ref: ref,
  };
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
  const batches = new Map<string, typeof items>();
  for (const item of items) {
    const { input, ref } = sourceOf(item.planned, item.link);
    const key = sourceKey(input, ref);
    batches.set(key, [...(batches.get(key) ?? []), item]);
  }
  const done: FetchedUnmanagedSkill[] = [];
  const lost: Failed[] = [];
  const fail = ({ planned, link }: (typeof items)[number], error: LibraryError) =>
    lost.push({ key: keyOf(planned), planned, error, link: sourceOf(planned, link).input ?? "", busy: false });
  for (const [key, members] of [...batches].sort(([a], [b]) => (a === noSource ? 1 : b === noSource ? -1 : 0))) {
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
