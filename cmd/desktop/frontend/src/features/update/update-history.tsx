// 历史：集合(或某个技能)的每次更新、跳过、回滚各一条，最新的在前。
// 更新记录可以回滚(在这个弹窗里换到回滚的确认页，不另叠一层)、重试没换上的地方;跳过的、被回滚掉的改动都可以找回来再更新
// (打开更新计划，默认勾上那几个):回滚掉的改动算作跳过，回滚记录和被回滚的那次更新上都有这个入口。
// 底部的「关闭」是普通按钮，不比记录上的操作显眼。
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueries } from "@tanstack/react-query";
import {
  ReinstallOutcome,
  UpdateRecordKind,
  type RecordedInstall,
  type RecordedSkill,
  type SkillID,
  type SkillVersion,
  type UpdateHistoryFilter,
  type UpdateRecord,
} from "@bindings/internal/library";
import { confirmLinkedRequests } from "@/components/app/link-notes";
import { formatDate } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { queryClient, toLibraryError, useSnapshot, updateHistoryQuery, versionQuery } from "@/lib/library";
import { eachPlace, placeCount, scopeLabel } from "@/lib/places";
import { shortRevision } from "@/lib/source";
import { Modal, useUI } from "@/lib/ui";
import { openUpdate, placeAgain } from "./update";
import { RollbackDialog, rollbackOutcome } from "./rollback";
import { putBack } from "./rollback-rules";
import { compareVersions } from "./version-compare";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import type { Tone } from "@/lib/level";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
import { Summary } from "@/components/app/parts";

function kindText(kind: string): string {
  const t = messages().history;
  const known: Record<string, string> = {
    [UpdateRecordKind.UpdateRecordUpdate]: t.update,
    [UpdateRecordKind.UpdateRecordSkip]: t.skip,
    [UpdateRecordKind.UpdateRecordRollback]: t.rollback,
  };
  return known[kind] ?? kind;
}

export function UpdateHistory({ filter, title, onClose }: { filter: UpdateHistoryFilter; title: string; onClose: () => void }) {
  const t = useT().history;
  // 读取失败和确实没有记录分开说：失败时给出原因和重试，不显示成"没有记录"。回滚、重试之后随写操作重读。
  const q = useQuery(updateHistoryQuery(filter));
  const failed = q.isError ? toLibraryError(q.error) : null;
  const records = q.data ?? null;
  // 正在回滚的那次更新：弹窗换成回滚的确认页，取消或做完回到历史。
  const [rolling, setRolling] = useState<UpdateRecord | null>(null);
  if (rolling) return <RollbackDialog record={rolling} onClose={() => setRolling(null)} />;

  return (
    <Modal title={title} wide onClose={onClose} footer={<Button onClick={onClose}>{t.close}</Button>}>
      {failed ? (
        <>
          <Notice alert className="whitespace-pre-line">
            {t.readFailed}
            {"\n"}
            {describeError(failed)}
          </Notice>
          <div>
            <Button onClick={() => void q.refetch()}>{t.retry}</Button>
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
            <Record
              key={r.id}
              record={r}
              all={records}
              onRollback={() => setRolling(r)}
              // 找回来再更新时关掉历史，更新计划不叠在历史上面。
              onReview={(ids) => {
                onClose();
                openUpdate(r.collection_id, { select: ids });
              }}
            />
          ))}
        </ListBox>
      )}
    </Modal>
  );
}

function Record({
  record,
  all,
  onRollback,
  onReview,
}: {
  record: UpdateRecord;
  all: UpdateRecord[];
  onRollback: () => void;
  onReview: (skills: SkillID[]) => void;
}) {
  const s = useSnapshot();
  const ui = useUI();
  const t = useT().history;
  const skills = record.skills ?? [];
  const installs = record.installs ?? [];
  // 项目里共用一个文件夹的几处合成一处计数，和下面列出的一致。
  const places = eachPlace(s, installs);
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
    update && places.length > 0 && t.reinstalled(places.length - placeCount(s, failed), placeCount(s, failed)),
    rollback &&
      places.length > 0 &&
      t.putBack(places.filter(putBack).length, places.length),
  ].filter(Boolean);

  const retry = async () => {
    const requests = failed.map((i) => ({ skill_id: i.skill_id, scope: i.scope, overwrite: false }));
    if (!(await confirmLinkedRequests(requests, t.retryTitle, t.retry))) return;
    const results = await placeAgain(record.id, requests);
    if (!results) return;
    const left = results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed).length;
    ui.toast(left ? t.stillLeft(left) : t.allPlaced, left ? "error" : "ok");
  };

  return (
    <ListRow className={cn("update-record items-start", rolledBack && "opacity-70")}>
      <Badge className="mt-px">{kindText(record.kind)}</Badge>
      <div className="flex min-w-0 grow flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-2 text-caption text-muted-foreground">
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
        {installs.length > 0 && <Places record={record} />}
        {(record.commits?.length ?? 0) > 0 && (
          <details>
            <Summary className="text-caption text-muted-foreground">{t.commits(record.commits!.length)}</Summary>
            <div className="mt-1 flex flex-col gap-0.5">
              {record.commits!.map((c) => (
                <div key={c.sha} className="flex gap-2 text-caption" title={c.message}>
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
          <Button onClick={retry}>
            {t.retryFailed}
          </Button>
        )}
        {update && !rolledBack && <Button onClick={onRollback}>{t.rollback}</Button>}
        {/* 跳过的改动、回滚掉的改动(算作跳过)都能找回来：打开更新计划，默认勾上那几个技能。
            用目录里的修改更新的那次回滚掉时不记跳过，那份修改不在更新计划里(还在取内容的那一处),不给这个入口。 */}
        {(record.kind === UpdateRecordKind.UpdateRecordSkip || ((rollback || (update && rolledBack)) && !(rollback ? target : record)?.from_scope)) &&
          s.collections.some((c) => c.id === record.collection_id) && (
            <Button
              onClick={() => onReview(skills.map((x) => x.skill_id))}
              title={record.kind === UpdateRecordKind.UpdateRecordSkip ? t.reviewTitle : t.reviewRolledBackTitle}
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
  const moved = record?.revision ? (record.skills ?? []).filter((x) => x.from_version_id !== x.to_version_id) : [];
  const versions = useQueries({ queries: moved.map((x) => versionQuery(x.from_version_id)) });
  // 全部读完(读不到的算完)再列，和逐个出现相比不跳动。
  if (versions.some((q) => q.isPending)) return [];
  return [...new Set(versions.flatMap((q) => (q.data?.revision ? [shortRevision(q.data.revision)] : [])))];
}

function reinstallText(outcome: string): { text: string; tone: Tone } | undefined {
  const t = messages().history;
  const known: Record<string, { text: string; tone: Tone }> = {
    [ReinstallOutcome.Reinstalled]: { text: t.placedOk, tone: "plain" },
    [ReinstallOutcome.ReinstallCurrent]: { text: t.placedCurrent, tone: "plain" },
    [ReinstallOutcome.ReinstallFailed]: { text: t.placedFailed, tone: "critical" },
  };
  return known[outcome];
}

// Places 逐处列出那次重新安装(更新)或装回(回滚)的结果，没换上、没装回的写明原因。
function Places({ record }: { record: UpdateRecord }) {
  const s = useSnapshot();
  const t = useT().history;
  const installs = record.installs ?? [];
  const names = new Map((record.skills ?? []).map((x) => [x.skill_id, x.name]));
  const name = (i: RecordedInstall) => names.get(i.skill_id) ?? s.skills.find((x) => x.id === i.skill_id)?.name ?? `#${i.skill_id}`;
  const outcome = (i: RecordedInstall) =>
    record.kind === UpdateRecordKind.UpdateRecordRollback ? rollbackOutcome(i.outcome) : (reinstallText(i.outcome) ?? { text: i.outcome, tone: "info" as Tone });
  return (
    <details className="update-places">
      <Summary className="text-caption text-muted-foreground">
        {t.places(record.kind === UpdateRecordKind.UpdateRecordRollback, placeCount(s, installs))}
      </Summary>
      <div className="mt-1 flex flex-col gap-0.5">
        {eachPlace(s, installs).map((i) => (
          <div key={`${i.skill_id}|${i.scope.agent}|${i.scope.project_id ?? 0}`} className="flex items-center gap-2 text-caption">
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

// SkillLine 是记录里的一个技能;更新和回滚可以打开比较弹窗看从哪个版本换到哪个版本改了什么(技能已移除时版本也不在了)。
function SkillLine({ skill, diffable }: { skill: RecordedSkill; diffable: boolean }) {
  const t = useT().history;
  const name = skill.from_name ? `${skill.from_name} → ${skill.name}` : skill.name;
  // 历史窗口关掉(这一行卸载)时中止还没读完的比较，不再弹出比较弹窗。
  const aborter = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    aborter.current = controller;
    return () => controller.abort();
  }, []);
  // 弹窗标题下面写两个版本的来源版本与日期(和记录上方读的是同一份缓存);读不到的一侧照常打开，只是少写那一侧。
  const open = async (origin: Element) => {
    const signal = aborter.current?.signal;
    const read = (id: number) => queryClient.fetchQuery(versionQuery(id)).catch(() => null);
    const [from, to] = await Promise.all([read(skill.from_version_id), read(skill.to_version_id)]);
    if (signal?.aborted) return;
    await compareVersions({
      title: t.compareTitle(name),
      note: t.fromTo(describe(from), describe(to)),
      from: skill.from_version_id,
      to: skill.to_version_id,
      origin,
      signal,
    });
  };
  const describe = (v: SkillVersion | null) => (v ? [v.revision && shortRevision(v.revision), formatDate(v.created_at)].filter(Boolean).join(" · ") : "");
  return (
    <div className="flex items-center gap-2">
      <b className="font-semibold">{name}</b>
      {diffable && skill.from_version_id !== skill.to_version_id && (
        <Button variant="ghost" size="inline" onClick={(e) => void open(e.currentTarget)}>
          {t.whatChanged}
        </Button>
      )}
    </div>
  );
}
