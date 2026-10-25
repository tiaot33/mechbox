// 回滚一次更新：库换回那次更新之前的版本，那次装过的地方装回去;没装回的列出原因。
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  RollbackOutcome,
  type RollbackInstall,
  type Scope,
  type SkillID,
  type UpdateRecordID,
} from "@bindings/internal/library";
import { linkWrites, placeKey } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { mergeProjectPlaces, scopeLabel, skillName } from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { LinkWriteNotes } from "@/components/app/link-notes";

export function rollbackOutcome(outcome: string): { text: string; tone: Tone } {
  const t = messages().update;
  const known: Record<string, { text: string; tone: Tone }> = {
    [RollbackOutcome.RollbackReinstalled]: { text: t.rollbackReinstalled, tone: "ok" },
    [RollbackOutcome.RollbackCurrent]: { text: t.rollbackCurrent, tone: "ok" },
    [RollbackOutcome.RollbackReinstalledSince]: { text: t.rollbackReinstalledSince, tone: "warn" },
    [RollbackOutcome.RollbackModified]: { text: t.rollbackModified, tone: "warn" },
    [RollbackOutcome.RollbackMissing]: { text: t.rollbackMissing, tone: "warn" },
    [RollbackOutcome.RollbackRootMissing]: { text: t.rollbackRootMissing, tone: "warn" },
    [RollbackOutcome.RollbackUninstalled]: { text: t.rollbackUninstalled, tone: "neutral" },
    [RollbackOutcome.RollbackRemoved]: { text: t.rollbackRemoved, tone: "neutral" },
    [RollbackOutcome.RollbackFailed]: { text: t.rollbackFailed, tone: "bad" },
  };
  return known[outcome] ?? { text: t.rollbackUnknown, tone: "warn" };
}

const putBack = (r: RollbackInstall) => r.outcome === RollbackOutcome.RollbackReinstalled || r.outcome === RollbackOutcome.RollbackCurrent;

// rollbackUpdate 回滚一次更新：库换回那次更新之前的版本，那次装过的地方(places)按磁盘内容核对后装回;没装回的列出原因。
// 那次装过的地方现在是符号链接的，装回时换成独立的一份，确认前现查一次说出来。
export async function rollbackUpdate(record: UpdateRecordID, places: { skill_id: SkillID; scope: Scope }[]) {
  const s = currentSnapshot();
  const wanted = new Set(places.map(placeKey));
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes({}), { reload: false });
  if (inspected === undefined) return;
  const writes = linkWrites(inspected ?? [], (p) => wanted.has(placeKey(p)));
  const t = messages().update;
  const ok = await ui.confirm({
    title: t.rollbackTitle,
    body: (
      <>
        <Hint>{t.rollbackHint}</Hint>
        <LinkWriteNotes {...writes} />
      </>
    ),
    confirm: t.rollback,
  });
  if (!ok) return;
  const result = await ui.run(() => LibraryService.Rollback(record), {
    onError: (err) => {
      if (err.code !== ErrorCode.FailedPrecondition) return false;
      ui.toast(t.alreadyRolledBack, "error");
      return true;
    },
    failed: (r) => (r.installs ?? []).some((x) => x.outcome === RollbackOutcome.RollbackFailed),
  });
  if (!result) return;
  const names = new Map<SkillID, string>([...(result.removed ?? []), ...(result.skills ?? [])].map((x) => [x.skill_id, x.name]));
  const name = (id: SkillID) => names.get(id) ?? skillName(s, id);
  const installs = result.installs ?? [];
  const back = mergeProjectPlaces(s, installs.filter(putBack)).length;
  const left = installs.filter((r) => !putBack(r));
  const removed = result.removed ?? [];
  const head = t.rolledBack(
    (result.skills ?? []).map((x) => x.name),
    back,
  );
  if (!left.length && !removed.length) {
    ui.toast(head);
    return;
  }
  await ui.open((close) => (
    <Modal
      title={t.rollbackDone}
      onClose={() => close()}
      footer={
        <Button variant="primary" onClick={() => close()}>
          {t.gotIt}
        </Button>
      }
    >
      <Notice>{t.rollbackLeft(head)}</Notice>
      <ListBox>
        {removed.map((x) => (
          <ListRow key={`removed-${x.skill_id}`}>
            <span className="grow">{x.name}</span>
            <Badge>{t.rollbackRemoved}</Badge>
          </ListRow>
        ))}
        {mergeProjectPlaces(s, left).map((r) => (
          <ListRow key={placeKey(r)}>
            <div className="min-w-0 grow">
              <div>
                {name(r.skill_id)} · {scopeLabel(s, r.scope)}
              </div>
              {r.error && <div className="text-xs whitespace-pre-line text-muted-foreground">{describeError(r.error)}</div>}
            </div>
            <Badge tone={rollbackOutcome(r.outcome).tone}>{rollbackOutcome(r.outcome).text}</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  ));
}

