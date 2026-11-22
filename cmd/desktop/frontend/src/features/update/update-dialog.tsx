// 更新计划：对一个集合发起，先看清改了什么，再一次确认同时完成更新和重新安装(issue #131,流程见 update.tsx)。
// 顶上写来源版本从哪到哪;下面左右两栏，各自滚动：左边是技能(有变化 / 改名 / 删除 / 没变化)、来源里的新技能和装出去的地方，
// 右边先是风险(新增或改动的脚本、新增的可执行文件)、GitHub 的提交说明，点了「看差异」的技能的文件差异接在后面。
// 看差异时左边的技能和要换的地方不动，不用来回滚动。弹窗大小固定，读取中和读完一样大。
// 可以跳过单个技能的这次改动，也可以跳过整个来源版本。从技能发起时只勾这一个，其余有变化的折叠;改名和删除照样显示，新技能不提。
import { useEffect, useRef, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { CollectionID, PlannedSkill, SkillID, UpdatePlan } from "@bindings/internal/library";
import { rememberPlanRisks } from "@/lib/plan-risks";
import { chosenInstallOn, defaultSkills, plannedInstalls } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { changedFiles } from "@/lib/diff";
import { useT } from "@/lib/i18n";
import { useSnapshot, type LibraryError } from "@/lib/library";
import { collectionName } from "@/features/update/update-rules";
import { folderKey } from "@/lib/installs";
import { revisionSummary, revisionText, shortRevision, sourceLabel } from "@/lib/source";
import { Modal, useBusy, useUI } from "@/lib/ui";
import { MarkIcon } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
import { Places } from "./places";
import { ChangedRow, DeletedRow, RenameRow } from "./plan-rows";
import { Commits, NewSkills, Risks, sectionClass } from "./plan-sections";
import { reportUpdates } from "./report";
import { SkillDiff } from "./skill-diff";
import { runUpdate } from "./update";
import { planSections, plannedName, stalePlan } from "./update-rules";

// usePlan 生成更新计划：打开时读一次来源，重试或计划失效后重新生成(replan);生成时写下的检查结论经 run 的重读带回主界面。
// 关掉弹窗时取消读取。onLoaded 在每次生成好之后调用，用来定默认勾选;patch 改动已生成的计划里的一个技能(跳过、回答"不是"之后)。
function usePlan(collectionID: CollectionID, onLoaded: (plan: UpdatePlan) => void) {
  const ui = useUI();
  const [generation, setGeneration] = useState(0);
  const [plan, setPlan] = useState<UpdatePlan | null>(null);
  const [failed, setFailed] = useState<LibraryError | null>(null);
  useEffect(() => {
    let cancelled = false;
    setPlan(null);
    setFailed(null);
    const call = LibraryService.PlanUpdate(collectionID);
    // 读取的进度画在弹窗里，顶栏的状态圈不跟着转。
    void ui.run(() => call, {
      busyMark: false,
      onError: (err) => {
        if (!cancelled) setFailed(err);
        return true;
      },
    }).then((p) => {
      if (!p) return;
      // 读到的风险也给更新页用，那一行不用再读一次来源。
      rememberPlanRisks(p);
      if (cancelled) return;
      setPlan(p);
      onLoaded(p);
    });
    return () => {
      cancelled = true;
      void call.cancel();
    };
    // onLoaded 只决定默认勾选，不放进依赖，否则每次渲染都会重新生成。
  }, [collectionID, generation]);
  const replan = () => setGeneration((n) => n + 1);
  const patch = (id: SkillID, change: Partial<PlannedSkill>) =>
    setPlan((prev) => prev && { ...prev, skills: (prev.skills ?? []).map((p) => (p.skill.id === id ? { ...p, ...change } : p)) });
  return { plan, failed, replan, patch };
}

export function UpdateDialog({
  collectionID,
  focus,
  select,
  onClose,
}: {
  collectionID: CollectionID;
  focus?: SkillID; // 从技能发起时的那个技能
  select?: SkillID[]; // 从跳过记录找回时默认勾上的技能
  onClose: () => void;
}) {
  const s = useSnapshot();
  const ui = useUI();
  const busy = useBusy();
  const tt = useT();
  const t = tt.plan;
  const label = collectionName(s, collectionID);
  const [checked, setChecked] = useState<Set<SkillID>>(new Set());
  const [installChoice, setInstallChoice] = useState<Map<string, boolean>>(new Map());
  const [expanded, setExpanded] = useState(!focus);
  const [viewing, setViewing] = useState<{ id: SkillID; path: string } | null>(null);
  const [removed, setRemoved] = useState<Set<SkillID>>(new Set());
  const [conflict, setConflict] = useState(false);
  const [working, setWorking] = useState(false);
  // 换了要看差异的技能时，差异的开头落在右栏看不见的地方(提交说明很长)才把右栏滚过去;看得见时不动，风险和提交说明留在上面。
  // 只滚右栏自己(不用 scrollIntoView:它会连带滚动外面不该动的几层)。
  const right = useRef<HTMLDivElement>(null);
  const diffAt = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const pane = right.current;
    const at = diffAt.current;
    if (!viewing || !pane || !at) return;
    const offset = at.getBoundingClientRect().top - pane.getBoundingClientRect().top;
    if (offset < 0 || offset > pane.clientHeight - 160) pane.scrollTo({ top: pane.scrollTop + offset - 4, behavior: "smooth" });
  }, [viewing?.id]);

  const { plan, failed, replan, patch } = usePlan(collectionID, (p) => {
    setChecked(defaultSkills(p, focus ? [focus] : select));
    setInstallChoice(new Map());
    setConflict(false);
  });

  if (!plan) {
    return (
      <Modal title={t.title(label)} large onClose={onClose}>
        {failed ? (
          <>
            <Notice alert className="whitespace-pre-line">
              {t.planFailed}
              {"\n"}
              {describeError(failed)}
            </Notice>
            <div>
              <Button onClick={replan}>{t.retry}</Button>
            </div>
          </>
        ) : (
          <Hint className="flex grow basis-0 items-center justify-center gap-1.5">
            <Spinner /> {t.reading}
          </Hint>
        )}
      </Modal>
    );
  }

  // 各节列哪些、要不要提醒留在旧的来源版本上，见 update-rules 的 planSections。
  const { skills, renamed, deleted, unchanged, shown, folded, movable, splitting } = planSections(plan, checked, removed, expanded, focus);
  // 一次写操作(更新、跳过、改名、移除、继续保留、不感兴趣)没结束前，其他写操作都不能点：
  // 更新读来源期间跳过成功，随后的更新仍会把这份改动换进库里、装出去。
  const locked = working || busy;
  const risks = movable.flatMap((p) => (p.risks ?? []).map((r) => ({ p, r })));
  const places = plannedInstalls(plan, checked);
  // 几个 Agent 共用一个文件夹时只能一起换，勾选按"技能 + 文件夹"算。
  const installOn = chosenInstallOn(installChoice);
  const name = (id: SkillID) => plannedName(plan, checked, id);
  const from = revisionSummary(skills.map((p) => p.skill));
  const to = plan.revision ? shortRevision(plan.revision) : "";
  const viewItem = viewing ? skills.find((p) => p.skill.id === viewing.id) : undefined;

  const view = (p: PlannedSkill, path?: string) => {
    if (viewing?.id === p.skill.id && !path) return setViewing(null);
    const first = path ?? p.risks?.[0]?.path ?? changedFiles(p.diff)[0]?.path;
    setViewing({ id: p.skill.id, path: first ?? "" });
  };

  // 计划生成后来源或库变了、或计划已失效时，提示重新生成，不弹错误。
  const onStale = (err: LibraryError) => {
    if (!stalePlan(err)) return false;
    setConflict(true);
    return true;
  };

  const skip = async (ids?: SkillID[]) => {
    const req = ids ? { plan: plan.id, skills: ids } : { plan: plan.id, whole_revision: true };
    const done = await ui.run(() => LibraryService.SkipUpdate(req).then(() => true), {
      success: ids ? t.skippedSkills(ids.map(name)) : t.skippedRevision(label),
      label: ids ? t.skipLabel(ids.map(name)) : t.skipRevisionLabel(label),
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
      label: t.rejectLabel(p.skill.name),
      onError: onStale,
    });
    if (!done) return;
    setChecked((set) => new Set([...set].filter((id) => id !== p.skill.id)));
    if (viewing?.id === p.skill.id) setViewing(null);
    patch(p.skill.id, { renamed_to: undefined, diff: undefined, risks: undefined });
  };

  const run = async () => {
    setWorking(true);
    const o = await runUpdate(plan, label, checked, installOn);
    setWorking(false);
    if (o === "conflict") return setConflict(true);
    if (!o) return;
    onClose();
    await reportUpdates([o]);
  };

  const check = (id: SkillID, on: boolean) => setChecked((set) => (on ? new Set(set).add(id) : new Set([...set].filter((x) => x !== id))));

  return (
    <Modal
      title={t.title(label)}
      large
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
        <Notice alert action={<Button onClick={replan}>{t.replan}</Button>}>
          {t.conflict}
        </Notice>
      )}
      <Hint>
        {sourceLabel(s, plan.source)}
        {from && (from.kind === "same" ? t.allAt(from.revision) : t.mixed(revisionText(from)))}
        {to && t.latest(to)}
      </Hint>

      <div className="update-panes flex min-h-0 grow basis-0 gap-4">
        {/* 左栏：要勾的技能和要换的地方。 */}
        <div className="update-left -mx-1 flex w-[400px] shrink-0 flex-col gap-2.5 overflow-y-auto px-1 pb-1 *:shrink-0">
          <div className={sectionClass}>{t.skills}</div>
          <ListBox>
            {shown.map((p) => (
              <ChangedRow
                key={p.skill.id}
                p={p}
                checked={checked.has(p.skill.id)}
                viewed={viewing?.id === p.skill.id}
                locked={locked}
                onCheck={(on) => check(p.skill.id, on)}
                onView={() => view(p)}
                onSkip={() => skip([p.skill.id])}
              />
            ))}
            {folded.length > 0 && (
              <ListRow compact>
                <span className="grow text-muted-foreground">{t.folded(folded.map((p) => p.skill.name))}</span>
                <Button variant="ghost" size="inline" onClick={() => setExpanded(true)}>
                  {t.expand}
                </Button>
              </ListRow>
            )}
            {renamed.map((p) => (
              <RenameRow
                key={p.skill.id}
                p={p}
                yes={checked.has(p.skill.id)}
                viewed={viewing?.id === p.skill.id}
                locked={locked}
                onCheck={(on) => check(p.skill.id, on)}
                onReject={() => reject(p)}
                onView={() => view(p)}
                onSkip={() => skip([p.skill.id])}
              />
            ))}
            {deleted.map((p) => (
              <DeletedRow
                key={p.skill.id}
                p={p}
                source={sourceLabel(s, plan.source)}
                locked={locked}
                onRemoved={() => setRemoved((set) => new Set(set).add(p.skill.id))}
              />
            ))}
            {unchanged.length > 0 && (
              <ListRow compact className="text-muted-foreground">
                {t.unchanged(unchanged.map((p) => p.skill.name))}
              </ListRow>
            )}
            {skills.length === 0 && <ListRow className="text-muted-foreground">{t.empty}</ListRow>}
          </ListBox>
          {/* 不在同一个来源版本上只是一个后果，不是风险：用提示一级的小字，不和脚本风险同级。 */}
          {splitting && (
            <Hint className="update-splitting flex items-start gap-1.5">
              <MarkIcon tone="info" className="mt-[3px]" />
              <span>{t.splitting(checked.size <= 3 ? [...checked].map(name) : checked.size, label)}</span>
            </Hint>
          )}

          {!focus && <NewSkills collectionID={collectionID} source={sourceLabel(s, plan.source)} locked={locked} />}

          {checked.size > 0 && (
            <>
              <div className={sectionClass}>{t.places}</div>
              <Places places={places} name={name} on={installOn} onToggle={(p) => setInstallChoice((m) => new Map(m).set(folderKey(p), !installOn(p)))} />
              <Hint>{t.uncheckedNote}</Hint>
            </>
          )}
        </div>

        {/* 右栏：先看风险和作者的意图，再看文件差异。 */}
        <div ref={right} className="update-right -mx-1 flex min-w-0 grow flex-col gap-2.5 overflow-y-auto px-1 pb-1 *:shrink-0">
          {movable.length > 0 && <Risks risks={risks} onView={view} />}
          {(plan.commits?.length ?? 0) > 0 && <Commits plan={plan} />}
          <div ref={diffAt}>
            {viewItem && viewing ? (
              <SkillDiff plan={plan} item={viewItem} path={viewing.path} onPath={(path) => setViewing({ id: viewing.id, path })} onError={onStale} />
            ) : (
              movable.length > 0 && <Hint className="update-diff-empty">{t.diffHere}</Hint>
            )}
          </div>
        </div>
      </div>
    </Modal>
  );
}
