// 一次确认入库之后，还有没入库的技能时的那一屏。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { FetchedUnmanagedSkill, PlannedUnmanagedSkill } from "@bindings/internal/library";
import { unmanagedSourceError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import { toLibraryError } from "@/lib/library";
import { Modal, ui } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { fetchedMessage, itemOf, type Failed } from "./fetch-unmanaged-parts";

// LeftDialog 是入库之后还有没入库的技能时的那一屏：列出原因，改来源链接后可以逐个重试，清空链接就作为没有来源的技能入库。
export function LeftDialog({
  fetched: initialFetched,
  failed: initialFailed,
  agentNames,
  close,
}: {
  fetched: FetchedUnmanagedSkill[];
  failed: Failed[];
  agentNames: (planned: PlannedUnmanagedSkill) => string;
  close: () => void;
}) {
  const t = useT().unmanaged;
  const [fetched, setFetched] = useState(initialFetched);
  const [failed, setFailed] = useState(initialFailed);

  // retry 用改过的来源链接重试一个没入库的技能;清空链接就作为没有来源的技能入库。按 key 找条目，几条一起重试也不会对错。
  const update = (key: string, change: Partial<Failed>) =>
    setFailed((list) =>
      list.map((x) => (x.key === key ? { ...x, ...change } : x)),
    );
  const retry = async (f: Failed) => {
    if (f.busy) return;
    update(f.key, { busy: true });
    try {
      const [r] =
        (await LibraryService.FetchUnmanagedSkills({
          skills: [itemOf(f.planned, f.link)],
        })) ?? [];
      if (r?.error) {
        update(f.key, { busy: false, error: r.error });
        return;
      }
      setFetched((list) => [...list, r]);
      setFailed((list) => list.filter((x) => x.key !== f.key));
      ui.reload();
    } catch (err) {
      update(f.key, { busy: false, error: toLibraryError(err) });
    }
  };

  return (
    <Modal
      title={t.leftTitle}
      wide
      onClose={close}
      footer={
        <Button variant="primary" onClick={close} autoFocus>
          {t.done}
        </Button>
      }
    >
      {fetched.length > 0 && (
        <Notice tone="ok">{fetchedMessage(fetched)}</Notice>
      )}
      <Hint>{t.leftHint(failed.length)}</Hint>
      <ListBox scroll className="fetch-unmanaged-failed">
        {failed.map((f) => (
          <ListRow key={f.key} className="flex-col items-stretch gap-1.5">
            <div className="flex min-w-0 items-center gap-2">
              <b className="font-semibold">{f.planned.name}</b>
              <span className="min-w-0 truncate text-[11px] text-muted-foreground">
                {agentNames(f.planned)}
              </span>
            </div>
            <p className="fetch-unmanaged-error text-[11.5px] whitespace-pre-line text-bad">
              {unmanagedSourceError(f.error, f.planned.name)}
            </p>
            <div className="flex min-w-0 items-center gap-2">
              <Input
                className="fetch-unmanaged-link grow font-mono"
                value={f.link}
                placeholder={t.linkPlaceholder}
                onChange={(e) => update(f.key, { link: e.target.value })}
                onKeyDown={(e) => e.key === "Enter" && void retry(f)}
              />
              <Button
                className="fetch-unmanaged-retry"
                loading={f.busy}
                onClick={() => void retry(f)}
              >
                {f.link.trim() ? t.retry : t.fetchWithoutSource}
              </Button>
            </div>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  );
}
