// 更新计划：对一个集合发起，先看清改了什么，再一次确认同时完成更新和重新安装(issue #131,流程见 lib/update.tsx)。
// 从上到下：来源版本从哪到哪、风险(新增或改动的脚本、新增的可执行文件)、GitHub 的提交说明、技能(有变化 / 改名 / 删除 / 没变化)
// 与文件差异、来源里的新技能、装出去的地方。可以跳过单个技能的这次改动，也可以跳过整个来源版本。
// 从技能发起时只勾这一个，其余有变化的折叠;改名和删除照样显示，新技能不提。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  PlannedChange,
  SkillScopeStatus,
  type CollectionID,
  type PlannedSkill,
  type SkillID,
  type SkillScopeInspection,
  type UpdatePlan,
} from "@bindings/internal/library";
import { removeSkill } from "@/lib/actions";
import { formatDate } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { agentName, folderKey, linkNote, revisionSummary, revisionText, scopeKey, scopeLabel, shortRevision, sourceLabel } from "@/lib/model";
import { Modal, useUI } from "@/lib/ui";
import {
  canInstall,
  collectionName,
  defaultInstallOn,
  defaultSkills,
  installKey,
  keepDeleted,
  notInterested,
  plannedInstalls,
  previewNewSkills,
  reportUpdates,
  riskLabel,
  riskSummary,
  runUpdate,
  stalePlan,
} from "@/lib/update";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
import { FileDiff } from "./file-diff";

const sectionClass = "mt-1 text-[13.5px] font-semibold";

export function UpdateDialog({
  s,
  collectionID,
  focus,
  select,
  onClose,
}: {
  s: Snapshot;
  collectionID: CollectionID;
  focus?: SkillID; // 从技能发起时的那个技能
  select?: SkillID[]; // 从跳过记录找回时默认勾上的技能
  onClose: () => void;
}) {
  const ui = useUI();
  const tt = useT();
  const t = tt.plan;
  const label = collectionName(s, collectionID);
  const collection = s.collections.find((c) => c.id === collectionID);
  const [generation, setGeneration] = useState(0);
  const [plan, setPlan] = useState<UpdatePlan | null>(null);
  const [failed, setFailed] = useState<LibraryError | null>(null);
  const [checked, setChecked] = useState<Set<SkillID>>(new Set());
  const [installChoice, setInstallChoice] = useState<Map<string, boolean>>(new Map());
  const [expanded, setExpanded] = useState(!focus);
  const [viewing, setViewing] = useState<{ id: SkillID; path: string } | null>(null);
  const [kept, setKept] = useState<Set<SkillID>>(new Set());
  const [removed, setRemoved] = useState<Set<SkillID>>(new Set());
  const [newSkills, setNewSkills] = useState<string[]>(collection?.new_skills ?? []);
  const [conflict, setConflict] = useState(false);
  const [working, setWorking] = useState(false);
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

  // 新技能按库里现在的集合说：生成计划、回答"不是"、入库或"不感兴趣"之后都会变，弹窗拿到的快照是打开时的。
  const refreshNew = useCallback(() => {
    const call = LibraryService.ListCollections();
    reads.current.add(call);
    void call
      .then(
        (list) => alive.current && setNewSkills(list?.find((c) => c.id === collectionID)?.new_skills ?? []),
        () => {},
      )
      .finally(() => reads.current.delete(call));
  }, [collectionID]);

  // 生成更新计划要读一次来源;生成时写下的检查结论经 run 的重读带回主界面。关掉弹窗时取消读取。
  useEffect(() => {
    let cancelled = false;
    setPlan(null);
    setFailed(null);
    const call = LibraryService.PlanUpdate(collectionID);
    void ui.run(() => call, {
      onError: (err) => {
        if (!cancelled) setFailed(err);
        return true;
      },
    }).then((p) => {
      if (cancelled || !p) return;
      setPlan(p);
      setChecked(defaultSkills(p, focus ? [focus] : select));
      setInstallChoice(new Map());
      setConflict(false);
      refreshNew();
    });
    return () => {
      cancelled = true;
      void call.cancel();
    };
    // ui 随忙碌状态变，不放进依赖，否则生成计划本身就会触发重新生成;focus / select 只决定默认勾选。
  }, [collectionID, generation]);

  const replan = () => setGeneration((n) => n + 1);

  if (!plan) {
    return (
      <Modal title={t.title(label)} wide onClose={onClose}>
        {failed ? (
          <>
            <Notice tone="bad" className="whitespace-pre-line">
              {t.planFailed}
              {"\n"}
              {describeError(failed)}
            </Notice>
            <div>
              <Button onClick={replan}>{t.retry}</Button>
            </div>
          </>
        ) : (
          <Hint className="flex items-center gap-1.5">
            <Spinner /> {t.reading}
          </Hint>
        )}
      </Modal>
    );
  }

  const skills = plan.skills ?? [];
  const changed = skills.filter((p) => p.change === PlannedChange.PlannedChanged);
  const renamed = skills.filter((p) => !!p.renamed_to);
  const deleted = skills.filter((p) => p.change === PlannedChange.PlannedDeleted && !p.renamed_to && !removed.has(p.skill.id));
  const unchanged = skills.filter((p) => p.change === PlannedChange.PlannedUnchanged);
  const shown = expanded ? changed : changed.filter((p) => p.skill.id === focus || checked.has(p.skill.id));
  const folded = changed.filter((p) => !shown.includes(p));
  const movable = [...changed, ...renamed];
  // 还没勾上的有变化或推测出改名的技能会留在旧的来源版本上。更新会换掉用户修改的(edited)默认就不勾，
  // 来源版本也可能本来就不知道，留着它们是预料之中的，不为此警告。
  const splitting = skills.length > 1 && checked.size > 0 && movable.some((p) => !checked.has(p.skill.id) && !p.edited);
  // 一次写操作(更新、跳过、改名、移除、继续保留、不感兴趣)没结束前，其他写操作都不能点：
  // 更新读来源期间跳过成功，随后的更新仍会把这份改动换进库里、装出去。
  const locked = working || ui.busy;
  const risks = movable.flatMap((p) => (p.risks ?? []).map((r) => ({ p, r })));
  const places = plannedInstalls(plan, checked);
  // 几个 Agent 共用一个文件夹时只能一起换，勾选按"技能 + 文件夹"算。
  const installOn = (p: SkillScopeInspection) => canInstall(p) && (installChoice.get(folderKey(p)) ?? defaultInstallOn(p));
  const name = (id: SkillID) => {
    const p = skills.find((x) => x.skill.id === id);
    if (!p) return `#${id}`;
    return p.renamed_to && checked.has(id) ? `${p.skill.name} → ${p.renamed_to}` : p.skill.name;
  };
  const from = revisionSummary(skills.map((p) => p.skill));
  const to = plan.revision ? shortRevision(plan.revision) : "";
  const viewItem = viewing ? skills.find((p) => p.skill.id === viewing.id) : undefined;

  const view = (p: PlannedSkill, path?: string) => {
    if (viewing?.id === p.skill.id && !path) return setViewing(null);
    const diff = p.diff;
    const first = path ?? p.risks?.[0]?.path ?? [...(diff?.changed ?? []), ...(diff?.added ?? []), ...(diff?.deleted ?? [])].sort((a, b) => Number(b === "SKILL.md") - Number(a === "SKILL.md"))[0];
    setViewing({ id: p.skill.id, path: first ?? "" });
  };

  // 计划生成后来源或库变了、或计划已失效时，提示重新生成，不弹错误。
  const onStale = (err: LibraryError) => {
    if (!stalePlan(err)) return false;
    setConflict(true);
    return true;
  };

  const patch = (id: SkillID, change: Partial<PlannedSkill>) =>
    setPlan((prev) => prev && { ...prev, skills: (prev.skills ?? []).map((p) => (p.skill.id === id ? { ...p, ...change } : p)) });

  const skip = async (ids?: SkillID[]) => {
    const req = ids ? { plan: plan.id, skills: ids } : { plan: plan.id, whole_revision: true };
    const done = await ui.run(() => LibraryService.SkipUpdate(req).then(() => true), {
      success: ids ? t.skippedSkills(ids.map(name)) : t.skippedRevision(label),
      onError: onStale,
    });
    if (!done) return;
    if (!ids) return onClose();
    for (const id of ids) patch(id, { skipped: true });
    setChecked((set) => new Set([...set].filter((id) => !ids.includes(id))));
  };

  const reject = async (p: PlannedSkill) => {
    const done = await ui.run(() => LibraryService.RejectRename(plan.id, p.skill.id).then(() => true), {
      success: t.rejected(p.skill.name, p.renamed_to),
      onError: onStale,
    });
    if (!done) return;
    refreshNew();
    setChecked((set) => new Set([...set].filter((id) => id !== p.skill.id)));
    if (viewing?.id === p.skill.id) setViewing(null);
    patch(p.skill.id, { renamed_to: undefined, diff: undefined, risks: undefined });
  };

  const run = async () => {
    setWorking(true);
    const o = await runUpdate(ui, plan, label, checked, installOn);
    setWorking(false);
    if (o === "conflict") return setConflict(true);
    if (!o) return;
    onClose();
    await reportUpdates(ui, s, [o]);
  };

  const check = (id: SkillID, on: boolean) => setChecked((set) => (on ? new Set(set).add(id) : new Set([...set].filter((x) => x !== id))));

  return (
    <Modal
      title={t.title(label)}
      wide
      onClose={onClose}
      footer={
        <>
          {movable.some((p) => !p.skipped) && (
            <Button disabled={locked} onClick={() => skip()} title={t.skipRevisionTitle}>
              {t.skipRevision}
            </Button>
          )}
          <span className="grow" />
          <Button onClick={onClose}>{t.cancel}</Button>
          <Button variant="primary" disabled={!checked.size || conflict || (locked && !working)} loading={working} onClick={run}>
            {t.updateN(checked.size)}
          </Button>
        </>
      }
    >
      {conflict && (
        <Notice tone="warn" className="flex items-center gap-2">
          <span className="grow">{t.conflict}</span>
          <Button size="sm" onClick={replan}>
            {t.replan}
          </Button>
        </Notice>
      )}
      <Hint>
        {sourceLabel(s, plan.source)}
        {from && (from.kind === "same" ? t.allAt(from.revision) : t.mixed(revisionText(from)))}
        {to && t.latest(to)}
      </Hint>

      {movable.length > 0 && (
        <>
          <div className={sectionClass}>{t.firstLook}</div>
          {risks.length ? (
            <Notice tone="warn" className="flex flex-col gap-0.5">
              {risks.map(({ p, r }) => (
                <button
                  key={`${p.skill.id}|${r.path}`}
                  type="button"
                  className="update-risk flex items-center gap-2 text-left hover:underline"
                  onClick={() => view(p, r.path)}
                >
                  <b className="font-semibold">{p.renamed_to ?? p.skill.name}</b>
                  <span>{riskLabel(r)}</span>
                  <span className="min-w-0 truncate font-mono text-[11px]">{r.path}</span>
                </button>
              ))}
            </Notice>
          ) : (
            <Hint>{t.noRisks}</Hint>
          )}
        </>
      )}

      {(plan.commits?.length ?? 0) > 0 && <Commits plan={plan} />}

      <div className={sectionClass}>{t.skills}</div>
      <ListBox>
        {shown.map((p) => (
          <ListRow key={p.skill.id} compact selected={checked.has(p.skill.id)} className="update-skill">
            <label className="flex min-w-0 grow cursor-pointer items-center gap-1.5">
              <input
                type="checkbox"
                className="shrink-0 accent-primary"
                checked={checked.has(p.skill.id)}
                onChange={(e) => check(p.skill.id, e.target.checked)}
              />
              <b className="font-semibold whitespace-nowrap">{p.skill.name}</b>
              {p.skipped ? <Badge title={t.skippedTitle}>{t.skipped}</Badge> : <Badge tone="warn">{t.changed}</Badge>}
              {(p.risks?.length ?? 0) > 0 && <Badge tone="bad">{riskSummary(p.risks ?? [])}</Badge>}
              {p.edited && (
                <span className="update-edited min-w-0 truncate text-[11px] text-muted-foreground" title={t.editedTitle}>
                  {t.edited}
                </span>
              )}
            </label>
            <Button variant="link" onClick={() => view(p)}>
              {viewing?.id === p.skill.id ? t.hideDiff : t.showDiff}
            </Button>
            {!p.skipped && (
              <Button variant="link" disabled={locked} onClick={() => skip([p.skill.id])} title={t.skipTitle}>
                {t.skip}
              </Button>
            )}
          </ListRow>
        ))}
        {folded.length > 0 && (
          <ListRow compact>
            <span className="grow text-muted-foreground">
              {t.folded(folded.map((p) => p.skill.name))}
            </span>
            <Button variant="link" onClick={() => setExpanded(true)}>
              {t.expand}
            </Button>
          </ListRow>
        )}
        {renamed.map((p) => {
          const yes = checked.has(p.skill.id);
          return (
            <ListRow key={p.skill.id} selected={yes} className="update-rename">
              <div className="min-w-0 grow">
                <div>
                  {t.renameQuestion(<b className="font-semibold">{p.skill.name}</b>, <b className="font-semibold">{p.renamed_to}</b>)}
                  {p.skipped && <Badge className="ml-1.5">{t.skipped}</Badge>}
                </div>
                <div className="text-[11px] text-muted-foreground">
                  {yes ? t.renameYes(p.renamed_to) : t.renameNo(p.renamed_to)}
                  {p.edited && <span className="update-edited">{t.renameEdited}</span>}
                </div>
              </div>
              {yes ? (
                <>
                  <Badge tone="accent">{t.sameOne}</Badge>
                  <Button variant="link" disabled={locked} onClick={() => check(p.skill.id, false)}>
                    {t.undo}
                  </Button>
                </>
              ) : (
                <>
                  <Button size="sm" disabled={locked} onClick={() => check(p.skill.id, true)}>
                    {t.yes}
                  </Button>
                  <Button size="sm" disabled={locked} onClick={() => reject(p)}>
                    {t.no}
                  </Button>
                </>
              )}
              <Button variant="link" onClick={() => view(p)}>
                {viewing?.id === p.skill.id ? t.hideDiff : t.showDiff}
              </Button>
              {!p.skipped && (
                <Button variant="link" disabled={locked} onClick={() => skip([p.skill.id])}>
                  {t.skip}
                </Button>
              )}
            </ListRow>
          );
        })}
        {deleted.map((p) => {
          const keep = kept.has(p.skill.id) || p.skill.kept_deleted;
          return (
            <ListRow key={p.skill.id} className="update-deleted">
              <div className="min-w-0 grow">
                <b className="font-semibold">{p.skill.name}</b>
                <span className="ml-1.5 text-[11px] text-muted-foreground">
                  {t.deletedHere(sourceLabel(s, plan.source))}
                </span>
              </div>
              {keep ? (
                <Badge>{t.keptDeleted}</Badge>
              ) : (
                <>
                  <Button
                    size="sm"
                    disabled={locked}
                    onClick={async () => {
                      if (await removeSkill(ui, s, p.skill.id)) setRemoved((set) => new Set(set).add(p.skill.id));
                    }}
                  >
                    {t.remove}
                  </Button>
                  <Button
                    size="sm"
                    disabled={locked}
                    onClick={async () => {
                      if (await keepDeleted(ui, s, p.skill.id)) setKept((set) => new Set(set).add(p.skill.id));
                    }}
                  >
                    {t.keep}
                  </Button>
                </>
              )}
            </ListRow>
          );
        })}
        {unchanged.length > 0 && (
          <ListRow compact className="text-muted-foreground">
            {t.unchanged(unchanged.map((p) => p.skill.name))}
          </ListRow>
        )}
        {skills.length === 0 && <ListRow className="text-muted-foreground">{t.empty}</ListRow>}
      </ListBox>
      {splitting && (
        <Notice tone="warn">
          {t.splitting(checked.size <= 3 ? [...checked].map(name) : checked.size, label)}
        </Notice>
      )}

      {viewItem && viewing && (
        <SkillDiff plan={plan} item={viewItem} path={viewing.path} onPath={(path) => setViewing({ id: viewing.id, path })} onError={onStale} />
      )}

      {!focus && newSkills.length > 0 && (
        <div className="update-new flex items-center gap-2 text-[12px] text-muted-foreground">
          <span className="grow">
            {tt.drawer.newSkills(sourceLabel(s, plan.source), newSkills)}
          </span>
          <Button variant="link" disabled={locked} onClick={() => previewNewSkills(ui, collectionID).then(refreshNew)}>
            {tt.drawer.goFetch}
          </Button>
          <Button
            variant="link"
            disabled={locked}
            onClick={async () => {
              if (await notInterested(ui, collectionID, newSkills)) refreshNew();
            }}
          >
            {tt.drawer.notInterested}
          </Button>
        </div>
      )}

      {checked.size > 0 && (
        <>
          <div className={sectionClass}>{t.places}</div>
          <Places s={s} places={places} name={name} on={installOn} onToggle={(p) => setInstallChoice((m) => new Map(m).set(folderKey(p), !installOn(p)))} />
          <Hint>{t.uncheckedNote}</Hint>
        </>
      )}
    </Modal>
  );
}

// Commits 是 GitHub 来源这段时间的提交说明，每条只显示第一行，最新的在前。
function Commits({ plan }: { plan: UpdatePlan }) {
  const t = useT().plan;
  const [all, setAll] = useState(false);
  const commits = plan.commits ?? [];
  const list = all ? commits : commits.slice(0, 6);
  return (
    <details open className="update-commits">
      <summary className={cn(sectionClass, "cursor-pointer")}>{t.commits(commits.length)}</summary>
      <ListBox className="mt-1.5">
        {list.map((c) => (
          <ListRow key={c.sha} compact title={c.message}>
            <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{c.sha.slice(0, 7)}</span>
            <span className="min-w-0 grow truncate">{c.message.split("\n")[0]}</span>
            <span className="shrink-0 text-[11px] text-subtle">
              {[c.author, c.time && formatDate(c.time)].filter(Boolean).join(" · ")}
            </span>
          </ListRow>
        ))}
        {!all && commits.length > list.length && (
          <ListRow compact>
            <Button variant="link" onClick={() => setAll(true)}>
              {t.moreCommits(commits.length - list.length)}
            </Button>
          </ListRow>
        )}
      </ListBox>
    </details>
  );
}

// SkillDiff 是一个技能这次改了哪些文件，点文件看内容差异:库里一侧读当前版本，来源一侧读更新计划。
// 读来源一侧时计划已失效(超时或来源变了)由 onError 交回弹窗，提示重新生成。
export function SkillDiff({
  plan,
  item,
  path,
  onPath,
  onError,
}: {
  plan: UpdatePlan;
  item: PlannedSkill;
  path: string;
  onPath: (path: string) => void;
  onError: (err: LibraryError) => boolean;
}) {
  const tt = useT();
  const t = tt.plan;
  const diff = item.diff;
  const files = [
    ...(diff?.added ?? []).map((p) => ["added", t.added, p] as const),
    ...(diff?.changed ?? []).map((p) => ["changed", t.modifiedFile, p] as const),
    ...(diff?.deleted ?? []).map((p) => ["deleted", t.deleted, p] as const),
  ];
  const kind = files.find((f) => f[2] === path)?.[0];
  const versionID = item.skill.version_id;
  const before = useCallback(() => LibraryService.GetVersionFile(versionID, path), [versionID, path]);
  const after = useCallback(() => LibraryService.GetUpdatePlanFile(plan.id, item.skill.id, path), [plan.id, item.skill.id, path]);
  const risk = useMemo(() => new Map((item.risks ?? []).map((r) => [r.path, riskLabel(r)])), [item.risks]);
  const color = { added: "text-ok-strong", changed: "text-warn-strong", deleted: "text-bad-strong" };
  return (
    <div className="update-diff flex flex-col gap-1.5 rounded-box border border-border-subtle bg-white/60 p-2.5">
      <b className="text-[12px]">{t.changesOf(item.renamed_to ? `${item.skill.name} → ${item.renamed_to}` : item.skill.name)}</b>
      <div className="flex flex-col">
        {files.map(([k, text, p]) => (
          <button
            key={p}
            type="button"
            data-diff={k}
            className={cn(
              "flex items-center gap-2 rounded-md px-1.5 py-0.5 text-left font-mono text-[11px] hover:bg-black/[0.04]",
              color[k],
              p === path && "bg-primary-soft hover:bg-primary-soft",
            )}
            onClick={() => onPath(p)}
          >
            <span className="w-7 shrink-0 font-sans [&:lang(en)]:w-14">{text}</span>
            <span className="min-w-0 grow truncate">{p}</span>
            {risk.has(p) && <Badge tone="bad">{risk.get(p)}</Badge>}
          </button>
        ))}
        {files.length === 0 && <span className="text-[11px] text-muted-foreground">{tt.common.sameContent}</span>}
      </div>
      {kind && <FileDiff path={path} before={kind === "added" ? null : before} after={kind === "deleted" ? null : after} onError={onError} />}
    </div>
  );
}

// Places 按地方列出勾选的技能装在哪里;同一处装了几个技能时各自一个勾选框，状态分开给。
export function Places({
  s,
  places,
  name,
  on,
  onToggle,
  modified,
}: {
  s: Snapshot;
  places: SkillScopeInspection[];
  name: (id: SkillID) => string;
  on: (p: SkillScopeInspection) => boolean;
  onToggle: (p: SkillScopeInspection) => void;
  modified?: string; // 用户改过的地方旁边的说明
}) {
  const t = useT().plan;
  if (!places.length) return <Hint>{t.notPlaced}</Hint>;
  // unchecked 是链着这一处、这次没勾的地方：换这一处之前，库先给它们换上独立的一份。
  const unchecked = (p: SkillScopeInspection) =>
    (p.linked_by ?? []).filter((scope) => {
      const other = places.find((x) => x.skill_id === p.skill_id && scopeKey(x.scope) === scopeKey(scope));
      return !other || !on(other);
    });
  const rows = new Map<string, SkillScopeInspection[]>();
  for (const p of places) rows.set(scopeKey(p.scope), [...(rows.get(scopeKey(p.scope)) ?? []), p]);
  return (
    <ListBox>
      {[...rows.entries()].map(([key, list]) => (
        <ListRow key={key} className="update-place items-start">
          <span className="w-[190px] shrink-0 truncate pt-px">{scopeLabel(s, list[0].scope)}</span>
          <div className="flex min-w-0 grow flex-col gap-0.5">
            {list.map((p) => (
              <label key={installKey(p)} className={cn("flex items-center gap-1.5", canInstall(p) && "cursor-pointer")}>
                <input type="checkbox" className="shrink-0 accent-primary" checked={on(p)} disabled={!canInstall(p)} onChange={() => onToggle(p)} />
                <span className="whitespace-nowrap">{name(p.skill_id)}</span>
                {p.status === SkillScopeStatus.SkillScopeModified && <Badge tone="warn">{modified ?? t.modifiedHere}</Badge>}
                {p.status === SkillScopeStatus.SkillScopeRootMissing && <Badge tone="bad">{t.rootMissing}</Badge>}
                {p.status === SkillScopeStatus.SkillScopeMissing && <Badge tone="warn">{t.folderMissing}</Badge>}
                {(p.shared_with?.length ?? 0) > 0 && (
                  <span className="text-[11px] text-muted-foreground">
                    {t.sharedFolder(p.shared_with!.map((code) => agentName(s, code)))}
                  </span>
                )}
                {p.link_target && on(p) && (
                  <span className="update-place-link min-w-0 text-[11px] break-all text-muted-foreground" title={p.link_target}>
                    {linkNote(s, p.link_target)}
                  </span>
                )}
                {on(p) && unchecked(p).length > 0 && (
                  <span className="update-place-kept min-w-0 text-[11px] text-muted-foreground">
                    {t.linkedHere(unchecked(p).map((scope) => scopeLabel(s, scope)))}
                  </span>
                )}
              </label>
            ))}
          </div>
        </ListRow>
      ))}
    </ListBox>
  );
}
