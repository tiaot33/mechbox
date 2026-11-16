// 回滚一次更新：先列出这次回滚具体会把哪些技能换回哪个版本、哪些地方装回旧版，确认后库换回那次更新之前的版本，
// 那次装过的地方装回去;没装回的列出原因。确认、进行和没装回的结果都在同一个弹窗里换页，不叠弹窗：
// 从历史里回滚时就在历史弹窗里换页(做完回到历史);从更新完成的提示里回滚时单独开一个。
import { useEffect, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  ReinstallOutcome,
  RollbackOutcome,
  type RecordedSkill,
  type RollbackResult,
  type SkillID,
  type SkillVersion,
  type UpdateHistoryFilter,
  type UpdateRecord,
  type UpdateRecordID,
} from "@bindings/internal/library";
import { linkWrites, placeKey } from "@/lib/consent";
import { formatDate } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { useSnapshot, versionQuery } from "@/lib/library";
import { mergeProjectPlaces, scopeLabel, shortRevision, skillName } from "@/lib/model";
import { Modal, ui, useBusy } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import type { Tone } from "@/lib/level";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
import { LinkWriteNotes } from "@/components/app/link-notes";
import { sectionClass } from "./plan-sections";

export function rollbackOutcome(outcome: string): { text: string; tone: Tone } {
  const t = messages().update;
  const known: Record<string, { text: string; tone: Tone }> = {
    [RollbackOutcome.RollbackReinstalled]: { text: t.rollbackReinstalled, tone: "plain" },
    [RollbackOutcome.RollbackCurrent]: { text: t.rollbackCurrent, tone: "plain" },
    [RollbackOutcome.RollbackReinstalledSince]: { text: t.rollbackReinstalledSince, tone: "plain" },
    [RollbackOutcome.RollbackModified]: { text: t.rollbackModified, tone: "warn" },
    [RollbackOutcome.RollbackMissing]: { text: t.rollbackMissing, tone: "critical" },
    [RollbackOutcome.RollbackRootMissing]: { text: t.rollbackRootMissing, tone: "critical" },
    [RollbackOutcome.RollbackUninstalled]: { text: t.rollbackUninstalled, tone: "plain" },
    [RollbackOutcome.RollbackRemoved]: { text: t.rollbackRemoved, tone: "plain" },
    [RollbackOutcome.RollbackFailed]: { text: t.rollbackFailed, tone: "critical" },
  };
  return known[outcome] ?? { text: t.rollbackUnknown, tone: "info" };
}

export const putBack = (r: { outcome: string }) => r.outcome === RollbackOutcome.RollbackReinstalled || r.outcome === RollbackOutcome.RollbackCurrent;

// rollbackUpdate 从更新完成的提示(或没换上的地方的弹窗)里回滚刚才那次更新：按 history 读出那条记录，单独开一个回滚弹窗。
export async function rollbackUpdate(record: UpdateRecordID, history: UpdateHistoryFilter) {
  const records = await ui.read(() => LibraryService.ListUpdateHistory(history));
  if (!records) return;
  const found = records.find((r) => r.id === record);
  if (!found || found.rolled_back_by) {
    ui.toast(messages().update.alreadyRolledBack, "error");
    return;
  }
  await ui.open((close) => <RollbackDialog record={found} onClose={() => close()} />);
}

// RollbackDialog 是回滚一次更新的弹窗：先是确认页，列出会换回的技能和装回旧版的地方;回滚完都装回了就提示一句并关上，
// 有没装回的、或技能已从库里移除的换成结果页列出原因。onClose 是确认页上的「取消」和看完结果(从历史里来的回到历史)。
export function RollbackDialog({ record, onClose }: { record: UpdateRecord; onClose: () => void }) {
  const s = useSnapshot();
  const [left, setLeft] = useState<{ result: RollbackResult; head: string } | null>(null);
  if (left) return <RollbackLeft {...left} onDone={onClose} />;
  return (
    <RollbackConfirm
      record={record}
      onBack={onClose}
      onRolledBack={(result) => {
        const installs = result.installs ?? [];
        const head = messages().update.rolledBack(
          (result.skills ?? []).map((x) => x.name),
          mergeProjectPlaces(s, installs.filter(putBack)).length,
        );
        if (installs.every(putBack) && !(result.removed ?? []).length) {
          ui.toast(head);
          onClose();
        } else setLeft({ result, head });
      }}
    />
  );
}

// 那次更新换上了的地方：回滚时要装回旧版的就是这些(之后又重新安装过、被改过的由回滚核对后不动)。
const placedBy = (record: UpdateRecord) =>
  (record.installs ?? []).filter((i) => i.outcome === ReinstallOutcome.Reinstalled || i.outcome === ReinstallOutcome.ReinstallCurrent);

function RollbackConfirm({
  record,
  onBack,
  onRolledBack,
}: {
  record: UpdateRecord;
  onBack: () => void;
  onRolledBack: (result: RollbackResult) => void;
}) {
  const s = useSnapshot();
  const busy = useBusy();
  const tt = useT();
  const t = tt.update;
  const [working, setWorking] = useState(false);
  const places = placedBy(record);
  const skills = (record.skills ?? []).filter((x) => x.from_version_id !== x.to_version_id);
  // 「现在」是技能库里这个技能当前的版本(之后又更新过的，回滚也一样换回那次之前);已从库里移除的写那次换上的版本。
  const current = (x: RecordedSkill) => s.skills.find((k) => k.id === x.skill_id);
  const versions = useQueries({
    queries: skills.flatMap((x) => [versionQuery(current(x)?.version_id ?? x.to_version_id), versionQuery(x.from_version_id)]),
  });
  const versionsRead = versions.every((q) => !q.isPending);
  // 那次装过的地方现在是符号链接的，装回时换成独立的一份：打开时现查一次，写在确认页上;查不成时不放行，给出重试。
  const [writes, setWrites] = useState<ReturnType<typeof linkWrites> | "failed" | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const wanted = new Set(places.map(placeKey));
    const call = LibraryService.InspectSkillScopes({});
    let alive = true;
    setWrites(null);
    void ui.read(() => call).then((inspected) => {
      if (alive) setWrites(inspected === undefined ? "failed" : linkWrites(inspected ?? [], (p) => wanted.has(placeKey(p))));
    });
    return () => {
      alive = false;
      void call.cancel();
    };
  }, [record.id, attempt]);

  const run = async () => {
    setWorking(true);
    const result = await ui.run(() => LibraryService.Rollback(record.id), {
      label: t.rollbackLabel(skills.map((x) => x.name)),
      onError: (err) => {
        if (err.code !== ErrorCode.FailedPrecondition) return false;
        ui.toast(t.alreadyRolledBack, "error");
        return true;
      },
      failed: (r) => (r.installs ?? []).some((x) => x.outcome === RollbackOutcome.RollbackFailed),
    });
    setWorking(false);
    if (result) onRolledBack(result);
  };

  // 回滚进行中不能退出：结果(哪些地方没装回)要在这个弹窗里接着列出来。
  const back = working ? () => {} : onBack;
  return (
    <Modal
      title={t.rollbackTitle}
      wide
      onClose={back}
      footer={
        <>
          <Button onClick={back} disabled={working}>
            {tt.common.cancel}
          </Button>
          <Button
            variant="primary"
            disabled={!writes || writes === "failed" || !versionsRead || (busy && !working)}
            loading={working}
            onClick={run}
          >
            {t.rollback}
          </Button>
        </>
      }
    >
      <Hint>{t.rollbackLead(formatDate(record.created_at))}</Hint>
      <div className={sectionClass}>{t.rollbackSkills}</div>
      <ListBox className="rollback-skills">
        {skills.map((x, i) => (
          <SkillBack key={x.skill_id} skill={x} name={current(x)?.name ?? x.name} now={versions[2 * i]?.data} back={versions[2 * i + 1]?.data} />
        ))}
        {skills.length === 0 && <ListRow className="text-muted-foreground">{t.rollbackNoSkills}</ListRow>}
      </ListBox>
      <div className={sectionClass}>{t.rollbackPlaces}</div>
      {places.length ? (
        <ListBox className="rollback-places">
          {mergeProjectPlaces(s, places).map((p) => (
            <ListRow key={placeKey(p)} compact>
              <span className="min-w-0 truncate">{scopeLabel(s, p.scope)}</span>
              <span className="min-w-0 truncate text-muted-foreground">{nameOf(record, p.skill_id) ?? skillName(s, p.skill_id)}</span>
            </ListRow>
          ))}
        </ListBox>
      ) : (
        <Hint>{t.rollbackNoPlaces}</Hint>
      )}
      <Hint>{record.from_scope ? t.rollbackHintScope(scopeLabel(s, record.from_scope)) : t.rollbackHint}</Hint>
      {writes === "failed" ? (
        <Notice alert action={<Button onClick={() => setAttempt((n) => n + 1)}>{t.retry}</Button>}>
          {t.rollbackCheckFailed}
        </Notice>
      ) : writes ? (
        <LinkWriteNotes {...writes} />
      ) : (
        <Hint className="flex items-center gap-1.5">
          <Spinner /> {t.rollbackChecking}
        </Hint>
      )}
    </Modal>
  );
}

const nameOf = (record: UpdateRecord, id: SkillID) => {
  const x = (record.skills ?? []).find((k) => k.skill_id === id);
  return x && (x.from_name || x.name);
};

// SkillBack 是确认页上的一个技能：现在是哪一版，回滚后换回哪一版(写来源版本;两边来源版本相同(如用目录里的修改更新过)时补上日期，
// 读不到来源版本时写日期);这次更新确认过改名的，名字也改回去。
function SkillBack({ skill, name, now, back }: { skill: RecordedSkill; name: string; now?: SkillVersion; back?: SkillVersion }) {
  const t = useT().update;
  const same = !!now?.revision && now.revision === back?.revision;
  const describe = (v: SkillVersion | undefined) => {
    if (!v) return "";
    const date = v.created_at ? formatDate(v.created_at) : "";
    if (!v.revision) return date;
    return same && date ? `${shortRevision(v.revision)} · ${date}` : shortRevision(v.revision);
  };
  return (
    <ListRow compact className="rollback-skill">
      <b className="min-w-0 truncate font-semibold">{skill.from_name && skill.from_name !== name ? t.renameBack(name, skill.from_name) : name}</b>
      <span className="grow" />
      <span className="shrink-0 font-mono text-caption text-muted-foreground">{t.versionBack(describe(now), describe(back))}</span>
    </ListRow>
  );
}

// RollbackLeft 是回滚之后的结果页：哪些地方没装回、哪些技能已从库里移除，各写原因。
function RollbackLeft({ result, head, onDone }: { result: RollbackResult; head: string; onDone: () => void }) {
  const s = useSnapshot();
  const t = useT().update;
  const names = new Map<SkillID, string>([...(result.removed ?? []), ...(result.skills ?? [])].map((x) => [x.skill_id, x.name]));
  const name = (id: SkillID) => names.get(id) ?? skillName(s, id);
  const left = (result.installs ?? []).filter((r) => !putBack(r));
  const removed = result.removed ?? [];
  return (
    <Modal
      title={t.rollbackDone}
      wide
      onClose={onDone}
      footer={
        <Button variant="primary" onClick={onDone}>
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
              {r.error && <div className="text-body whitespace-pre-line text-muted-foreground">{describeError(r.error)}</div>}
            </div>
            <Badge tone={rollbackOutcome(r.outcome).tone}>{rollbackOutcome(r.outcome).text}</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  );
}
