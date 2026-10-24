// 历史：集合(或某个技能)的每次更新、跳过、回滚各一条，最新的在前。
// 更新记录可以回滚、重试没换上的地方;跳过的记录可以找回来再更新(打开更新计划，默认勾上那几个)。
import { useEffect, useRef, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ReinstallOutcome,
  UpdateRecordKind,
  type RecordedInstall,
  type RecordedSkill,
  type SkillVersion,
  type UpdateHistoryFilter,
  type UpdateRecord,
  type VersionDiff,
} from "@bindings/internal/library";
import { confirmLinkedRequests } from "@/lib/actions";
import { formatDate } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { toLibraryError, type LibraryError, type Snapshot } from "@/lib/library";
import { mergeProjectPlaces, scopeLabel, shortRevision } from "@/lib/model";
import { Modal, useUI } from "@/lib/ui";
import { openUpdate, rollbackOutcome, rollbackUpdate } from "@/lib/update";
import { cn } from "@/lib/utils";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";

function kindText(kind: string): { text: string; tone: Tone } | undefined {
  const t = messages().history;
  const known: Record<string, { text: string; tone: Tone }> = {
    [UpdateRecordKind.UpdateRecordUpdate]: { text: t.update, tone: "accent" },
    [UpdateRecordKind.UpdateRecordSkip]: { text: t.skip, tone: "neutral" },
    [UpdateRecordKind.UpdateRecordRollback]: { text: t.rollback, tone: "warn" },
  };
  return known[kind];
}

export function UpdateHistory({ s, filter, title, onClose }: { s: Snapshot; filter: UpdateHistoryFilter; title: string; onClose: () => void }) {
  const t = useT().history;
  const [records, setRecords] = useState<UpdateRecord[] | null>(null);
  const [failed, setFailed] = useState<LibraryError | null>(null);
  const [generation, setGeneration] = useState(0);

  // 读取失败和确实没有记录分开说：失败时给出原因和重试，不显示成"没有记录"。
  useEffect(() => {
    let cancelled = false;
    setFailed(null);
    const call = LibraryService.ListUpdateHistory(filter);
    call.then(
      (r) => !cancelled && setRecords(r ?? []),
      (e) => {
        if (cancelled) return;
        setRecords(null);
        setFailed(toLibraryError(e));
      },
    );
    return () => {
      cancelled = true;
      void call.cancel();
    };
  }, [filter, generation]);

  return (
    <Modal
      title={title}
      wide
      onClose={onClose}
      footer={
        <Button variant="primary" onClick={onClose}>
          {t.close}
        </Button>
      }
    >
      {failed ? (
        <>
          <Notice tone="bad" className="whitespace-pre-line">
            {t.readFailed}
            {"\n"}
            {describeError(failed)}
          </Notice>
          <div>
            <Button onClick={() => setGeneration((n) => n + 1)}>{t.retry}</Button>
          </div>
        </>
      ) : records === null ? (
        <Hint className="flex items-center gap-1.5">
          <Spinner /> {t.reading}
        </Hint>
      ) : records.length === 0 ? (
        <Hint>{t.empty}</Hint>
      ) : (
        <ListBox>
          {records.map((r) => (
            <Record key={r.id} s={s} record={r} all={records} onChange={() => setGeneration((n) => n + 1)} />
          ))}
        </ListBox>
      )}
    </Modal>
  );
}

function Record({ s, record, all, onChange }: { s: Snapshot; record: UpdateRecord; all: UpdateRecord[]; onChange: () => void }) {
  const ui = useUI();
  const t = useT().history;
  const kind = kindText(record.kind) ?? { text: record.kind, tone: "neutral" as Tone };
  const skills = record.skills ?? [];
  const installs = record.installs ?? [];
  // 项目里共用一个文件夹的几处合成一处计数，和下面列出的一致。
  const places = mergeProjectPlaces(s, installs);
  const update = record.kind === UpdateRecordKind.UpdateRecordUpdate;
  const rollback = record.kind === UpdateRecordKind.UpdateRecordRollback;
  const failed = update ? installs.filter((i) => i.outcome === ReinstallOutcome.ReinstallFailed) : [];
  const rolledBack = !!record.rolled_back_by;
  const target = rollback ? all.find((r) => r.id === record.rolls_back) : undefined;
  const from = useFromRevisions(update ? record : undefined);

  const facts = [
    record.from_scope && t.fromScope(scopeLabel(s, record.from_scope)),
    // 回滚记录的来源版本是被回滚那次更新换到的，不是回滚后的，所以只说回滚了哪次。
    record.revision &&
      !rollback &&
      (update
        ? t.movedTo(from, shortRevision(record.revision))
        : t.revision(shortRevision(record.revision))),
    record.whole_revision && t.wholeRevision,
    rollback && (target ? t.rolledBackAt(formatDate(target.created_at)) : t.rolledBackOne),
    update && places.length > 0 && t.reinstalled(places.length - mergeProjectPlaces(s, failed).length, mergeProjectPlaces(s, failed).length),
    rollback &&
      places.length > 0 &&
      t.putBack(places.filter((i) => rollbackOutcome(i.outcome).tone === "ok").length, places.length),
  ].filter(Boolean);

  const retry = async () => {
    const requests = failed.map((i) => ({ skill_id: i.skill_id, scope: i.scope, overwrite: false }));
    if (!(await confirmLinkedRequests(ui, s, requests, t.retryTitle, t.retry))) return;
    const results = await ui.run(() => LibraryService.RetryUpdateInstalls(record.id, requests));
    if (!results) return;
    const left = results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed).length;
    ui.toast(left ? t.stillLeft(left) : t.allPlaced, left ? "error" : "ok");
    onChange();
  };

  return (
    <ListRow className={cn("update-record items-start", rolledBack && "opacity-70")}>
      <Badge tone={kind.tone} className="mt-px">
        {kind.text}
      </Badge>
      <div className="flex min-w-0 grow flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-2 text-[11px] text-muted-foreground">
          <span className="text-foreground">{formatDate(record.created_at)}</span>
          {facts.map((f) => (
            <span key={String(f)}>{f}</span>
          ))}
          {rolledBack && <Badge>{t.rolledBack}</Badge>}
        </div>
        <div className="flex flex-col gap-0.5">
          {skills.map((x) => (
            <SkillLine key={x.skill_id} skill={x} diffable={update || rollback} />
          ))}
        </div>
        {installs.length > 0 && <Places s={s} record={record} />}
        {(record.commits?.length ?? 0) > 0 && (
          <details>
            <summary className="cursor-pointer text-[11px] text-muted-foreground">{t.commits(record.commits!.length)}</summary>
            <div className="mt-1 flex flex-col gap-0.5">
              {record.commits!.map((c) => (
                <div key={c.sha} className="flex gap-2 text-[11px]" title={c.message}>
                  <span className="shrink-0 font-mono text-muted-foreground">{c.sha.slice(0, 7)}</span>
                  <span className="min-w-0 truncate">{c.message.split("\n")[0]}</span>
                </div>
              ))}
            </div>
          </details>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        {update && !rolledBack && failed.length > 0 && (
          <Button size="sm" onClick={retry}>
            {t.retryFailed}
          </Button>
        )}
        {update && !rolledBack && (
          <Button
            size="sm"
            onClick={async () => {
              await rollbackUpdate(ui, s, record.id, record.installs ?? []);
              onChange();
            }}
          >
            {t.rollback}
          </Button>
        )}
        {record.kind === UpdateRecordKind.UpdateRecordSkip && s.collections.some((c) => c.id === record.collection_id) && (
          <Button
            size="sm"
            onClick={() => openUpdate(ui, s, record.collection_id, { select: skills.map((x) => x.skill_id) })}
            title={t.reviewTitle}
          >
            {t.review}
          </Button>
        )}
      </div>
    </ListRow>
  );
}

// useFromRevisions 是一次更新之前各技能所在的来源版本(去重);记录只带换到的来源版本，之前的从各技能的旧版本读。
// 技能已从库里移除时旧版本也不在了，读不到的不列。
function useFromRevisions(record: UpdateRecord | undefined): string[] {
  const [revisions, setRevisions] = useState<string[]>([]);
  const id = record?.id;
  useEffect(() => {
    if (!record?.revision) return;
    let cancelled = false;
    const calls = (record.skills ?? []).filter((x) => x.from_version_id !== x.to_version_id).map((x) => LibraryService.GetVersion(x.from_version_id));
    void Promise.allSettled(calls).then((results) => {
      if (cancelled) return;
      const revs = results.flatMap((r) => (r.status === "fulfilled" && r.value.revision ? [shortRevision(r.value.revision)] : []));
      setRevisions([...new Set(revs)]);
    });
    return () => {
      cancelled = true;
      calls.forEach((c) => void c.cancel());
    };
    // 记录本身不会变，按 ID 读一次。
  }, [id]);
  return revisions;
}

function reinstallText(outcome: string): { text: string; tone: Tone } | undefined {
  const t = messages().history;
  const known: Record<string, { text: string; tone: Tone }> = {
    [ReinstallOutcome.Reinstalled]: { text: t.placedOk, tone: "ok" },
    [ReinstallOutcome.ReinstallCurrent]: { text: t.placedCurrent, tone: "ok" },
    [ReinstallOutcome.ReinstallFailed]: { text: t.placedFailed, tone: "bad" },
  };
  return known[outcome];
}

// Places 逐处列出那次重新安装(更新)或装回(回滚)的结果，没换上、没装回的写明原因。
function Places({ s, record }: { s: Snapshot; record: UpdateRecord }) {
  const t = useT().history;
  const installs = record.installs ?? [];
  const names = new Map((record.skills ?? []).map((x) => [x.skill_id, x.name]));
  const name = (i: RecordedInstall) => names.get(i.skill_id) ?? s.skills.find((x) => x.id === i.skill_id)?.name ?? `#${i.skill_id}`;
  const outcome = (i: RecordedInstall) =>
    record.kind === UpdateRecordKind.UpdateRecordRollback ? rollbackOutcome(i.outcome) : (reinstallText(i.outcome) ?? { text: i.outcome, tone: "warn" as Tone });
  return (
    <details className="update-places">
      <summary className="cursor-pointer text-[11px] text-muted-foreground">
        {t.places(record.kind === UpdateRecordKind.UpdateRecordRollback, mergeProjectPlaces(s, installs).length)}
      </summary>
      <div className="mt-1 flex flex-col gap-0.5">
        {mergeProjectPlaces(s, installs).map((i) => (
          <div key={`${i.skill_id}|${i.scope.agent}|${i.scope.project_id ?? 0}`} className="flex items-center gap-2 text-[11px]">
            <span className="min-w-0 truncate">
              {name(i)} · {scopeLabel(s, i.scope)}
            </span>
            <Badge tone={outcome(i).tone}>{outcome(i).text}</Badge>
            {i.error && <span className="truncate text-muted-foreground">{describeError({ code: i.error, message: "" })}</span>}
          </div>
        ))}
      </div>
    </details>
  );
}

// SkillLine 是记录里的一个技能;更新和回滚可以展开看从哪个版本换到哪个版本改了哪些文件(技能已移除时版本也不在了)。
function SkillLine({ skill, diffable }: { skill: RecordedSkill; diffable: boolean }) {
  const ui = useUI();
  const tt = useT();
  const t = tt.history;
  const [diff, setDiff] = useState<VersionDiff | null>(null);
  const [versions, setVersions] = useState<(SkillVersion | null)[]>([]);
  const alive = useRef(true);
  const reads = useRef(new Set<CancellablePromise<unknown>>());
  useEffect(() => {
    alive.current = true;
    const pending = reads.current;
    return () => {
      alive.current = false;
      for (const call of pending) void call.cancel();
      pending.clear();
    };
  }, []);
  const track = <T,>(call: CancellablePromise<T>) => {
    reads.current.add(call);
    void call.finally(() => reads.current.delete(call)).catch(() => {});
    return call;
  };
  const toggle = async () => {
    if (diff) return setDiff(null);
    const d = await ui.run(() => track(LibraryService.DiffVersions(skill.from_version_id, skill.to_version_id)), { reload: false });
    if (!d || !alive.current) return;
    setDiff(d);
    const read = (id: number) => track(LibraryService.GetVersion(id)).then((v) => v, () => null);
    const both = await Promise.all([read(skill.from_version_id), read(skill.to_version_id)]);
    if (alive.current) setVersions(both);
  };
  const describe = (v: SkillVersion | null | undefined) => (v ? [v.revision && shortRevision(v.revision), formatDate(v.created_at)].filter(Boolean).join(" · ") : "");
  const files = diff
    ? [
        ...(diff.added ?? []).map((p) => ["added", t.added, p] as const),
        ...(diff.changed ?? []).map((p) => ["changed", t.changed, p] as const),
        ...(diff.deleted ?? []).map((p) => ["deleted", t.deleted, p] as const),
      ]
    : [];
  const color = { added: "text-ok-strong", changed: "text-warn-strong", deleted: "text-bad-strong" };
  return (
    <div>
      <div className="flex items-center gap-2">
        <b className="font-semibold">{skill.from_name ? `${skill.from_name} → ${skill.name}` : skill.name}</b>
        {diffable && skill.from_version_id !== skill.to_version_id && (
          <Button variant="link" onClick={toggle}>
            {diff ? t.collapse : t.whatChanged}
          </Button>
        )}
      </div>
      {diff && (
        <div className="pl-2">
          {versions.length > 0 && (
            <div className="text-[11px] text-muted-foreground">
              {t.fromTo(describe(versions[0]), describe(versions[1]))}
            </div>
          )}
          {files.map(([k, text, p]) => (
            <div key={p} data-diff={k} className={cn("font-mono text-[11px]", color[k])}>
              {text} {p}
            </div>
          ))}
          {files.length === 0 && <div className="text-[11px] text-muted-foreground">{tt.common.sameContent}</div>}
        </div>
      )}
    </div>
  );
}
