// 更新完成后的汇报：都换上了用 toast,一次更新时带"回滚";有地方没换上时列出原因，可以单独重试。
import { useEffect, useRef, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import type { ReinstallResult } from "@bindings/internal/library";
import { placeKey } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { mergeProjectPlaces, scopeLabel, skillName } from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { confirmLinkedRequests } from "@/components/app/link-notes";
import { failedOnly, placeAgain, type UpdateOutcome } from "./update";
import { rollbackUpdate } from "./rollback";

function summarize(o: UpdateOutcome): string {
  const t = messages().update;
  if (o.from) return t.summaryFromScope(o.from, o.label, o.placed, o.keptEdited, o.keptMissing);
  return t.summary(o.local, o.label, o.changed, o.placed, o.keptEdited, o.keptMissing);
}

// reportUpdates 汇报一次或几次更新：都换上了用 toast,一次更新时带"回滚";有地方没换上时列出原因，可以单独重试。
export async function reportUpdates(outcomes: UpdateOutcome[]) {
  if (!outcomes.length) return;
  const t = messages().update;
  if (outcomes.every((o) => !o.failed.length)) {
    if (outcomes.length === 1) {
      const o = outcomes[0];
      ui.toast(summarize(o), "ok", o.record && o.changed ? { label: t.rollback, onClick: () => void rollbackUpdate(o.record, o.requests) } : undefined);
    } else {
      ui.toast([...outcomes.map(summarize), t.rollbackInHistory].join("\n"));
    }
    return;
  }
  await ui.open((close) => <FailedPlaces outcomes={outcomes} close={() => close()} />);
}

// FailedPlaces 列出没换上的地方和原因;"重试"把它们单独再换一次，结果算进那次更新(回滚时一起装回去)。
// 重试中途关掉弹窗时取消正在换的这一次，后面的集合不再继续。
function FailedPlaces({ outcomes, close }: { outcomes: UpdateOutcome[]; close: () => void }) {
  const s = useSnapshot();
  const t = useT().update;
  const [failed, setFailed] = useState(() => outcomes.map((o) => o.failed));
  const [working, setWorking] = useState(false);
  const alive = useRef(true);
  const call = useRef<CancellablePromise<ReinstallResult[] | null> | null>(null);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      void call.current?.cancel();
    };
  }, []);
  const left = mergeProjectPlaces(s, failed.flat()).length;
  const retry = async () => {
    const again = outcomes.flatMap((o, i) => o.requests.filter((r) => failed[i].some((f) => placeKey(f) === placeKey(r))));
    if (!(await confirmLinkedRequests(again, t.retryTitle, t.retry))) return;
    if (!alive.current) return;
    setWorking(true);
    const next: ReinstallResult[][] = [];
    for (const [i, o] of outcomes.entries()) {
      const again = o.requests.filter((r) => failed[i].some((f) => placeKey(f) === placeKey(r)));
      const viaRecord = o.record ? again.filter((r) => !o.plain.has(placeKey(r))) : [];
      const left: ReinstallResult[] = [];
      for (const [batch, record] of [
        [viaRecord, o.record],
        [again.filter((r) => !viaRecord.includes(r)), 0],
      ] as const) {
        if (!batch.length) continue;
        const results = await placeAgain(record, batch, (c) => (call.current = c));
        call.current = null;
        if (!alive.current) return;
        left.push(...(results ? failedOnly(results) : failed[i].filter((f) => batch.some((r) => placeKey(r) === placeKey(f)))));
      }
      next.push(left);
    }
    setWorking(false);
    setFailed(next);
    if (!next.flat().length) {
      ui.toast(t.allPlaced);
      close();
    }
  };
  // 只补装、没换版本的(目录没变时的重新读取)不提供回滚：这次没有可退回的改动。
  const single = outcomes.length === 1 && outcomes[0].record && outcomes[0].changed ? outcomes[0] : undefined;
  return (
    <Modal
      title={t.failedTitle}
      onClose={close}
      footer={
        <>
          {single && (
            <Button
              onClick={() => {
                close();
                void rollbackUpdate(single.record, single.requests);
              }}
            >
              {t.rollback}
            </Button>
          )}
          <span className="grow" />
          <Button onClick={close}>{t.gotIt}</Button>
          <Button variant="primary" loading={working} disabled={!left} onClick={retry}>
            {t.retryFailed(left)}
          </Button>
        </>
      }
    >
      <Notice className="whitespace-pre-line">
        {outcomes.map(summarize).join("\n")}
        {"\n"}
        {t.failedNotice}
      </Notice>
      <ListBox>
        {mergeProjectPlaces(s, failed.flat()).map((r) => (
          <ListRow key={placeKey(r)}>
            <div className="min-w-0 grow">
              <div>
                {skillName(s, r.skill_id)} · {scopeLabel(s, r.scope)}
              </div>
              {r.error && <div className="text-xs whitespace-pre-line text-muted-foreground">{describeError(r.error)}</div>}
            </div>
            <Badge tone="bad">{t.notPlaced}</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  );
}
