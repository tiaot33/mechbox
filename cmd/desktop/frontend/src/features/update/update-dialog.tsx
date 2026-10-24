// 更新计划：对一个集合发起，先看清改了什么，再一次确认同时完成更新和重新安装(issue #131,流程见 update.tsx)。
// 从上到下：来源版本从哪到哪、风险(新增或改动的脚本、新增的可执行文件)、GitHub 的提交说明、技能(有变化 / 改名 / 删除 / 没变化)
// 与文件差异、来源里的新技能、装出去的地方。可以跳过单个技能的这次改动，也可以跳过整个来源版本。
// 从技能发起时只勾这一个，其余有变化的折叠;改名和删除照样显示，新技能不提。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  PlannedChange,
  type CollectionID,
  type PlannedSkill,
  type SkillID,
  type SkillScopeInspection,
  type UpdatePlan,
} from "@bindings/internal/library";
import { canInstall, defaultInstallOn, defaultSkills, plannedInstalls } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import { useSnapshot, type LibraryError } from "@/lib/library";
import { collectionName, folderKey, revisionSummary, revisionText, shortRevision, sourceLabel } from "@/lib/model";
import { Modal, useBusy, useUI } from "@/lib/ui";
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
import { runUpdate, stalePlan } from "./update";

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
    void ui.run(() => call, {
      onError: (err) => {
        if (!cancelled) setFailed(err);
        return true;
      },
    }).then((p) => {
      if (cancelled || !p) return;
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

  const { plan, failed, replan, patch } = usePlan(collectionID, (p) => {
    setChecked(defaultSkills(p, focus ? [focus] : select));
    setInstallChoice(new Map());
    setConflict(false);
  });

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
  const locked = working || busy;
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

      {movable.length > 0 && <Risks risks={risks} onView={view} />}

      {(plan.commits?.length ?? 0) > 0 && <Commits plan={plan} />}

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
            <span className="grow text-muted-foreground">
              {t.folded(folded.map((p) => p.skill.name))}
            </span>
            <Button variant="link" onClick={() => setExpanded(true)}>
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
      {splitting && (
        <Notice tone="warn">
          {t.splitting(checked.size <= 3 ? [...checked].map(name) : checked.size, label)}
        </Notice>
      )}

      {viewItem && viewing && (
        <SkillDiff plan={plan} item={viewItem} path={viewing.path} onPath={(path) => setViewing({ id: viewing.id, path })} onError={onStale} />
      )}

      {!focus && <NewSkills collectionID={collectionID} source={sourceLabel(s, plan.source)} locked={locked} />}

      {checked.size > 0 && (
        <>
          <div className={sectionClass}>{t.places}</div>
          <Places places={places} name={name} on={installOn} onToggle={(p) => setInstallChoice((m) => new Map(m).set(folderKey(p), !installOn(p)))} />
          <Hint>{t.uncheckedNote}</Hint>
        </>
      )}
    </Modal>
  );
}
