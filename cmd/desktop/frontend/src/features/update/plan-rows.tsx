// 更新计划弹窗里技能列表的几种行：有变化的、推测出改名的、来源里已删除的。
import { useState } from "react";
import type { PlannedSkill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { keepDeleted, removeSkill } from "@/features/skill/remove";
import { Badge, MarkIcon } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListRow } from "@/components/ui/list";
import { cn } from "@/lib/utils";
import { riskSummary } from "@/lib/risks";

interface RowActions {
  viewed: boolean; // 这个技能的差异正展开着
  locked: boolean; // 有写操作进行中，其他写操作不能点
  onCheck: (on: boolean) => void;
  onView: () => void;
  onSkip: () => void;
}

// ChangedRow 是有变化的技能：勾选才更新;标出风险、跳过过的、更新会换掉用户修改的，可以看差异、跳过这次改动。
// 勾选是这一行的主操作;看差异、跳过是轻按钮。
export function ChangedRow({ p, checked, viewed, locked, onCheck, onView, onSkip }: RowActions & { p: PlannedSkill; checked: boolean }) {
  const t = useT().plan;
  return (
    <ListRow compact selected={checked} className="update-skill">
      <label className="flex min-w-0 grow cursor-pointer items-center gap-1.5">
        <input type="checkbox" className="shrink-0 accent-primary" checked={checked} onChange={(e) => onCheck(e.target.checked)} />
        {/* 名字不让给后面的标签：最多占一半，放不下的风险标签自己截短(悬停看全)。 */}
        <b className="max-w-1/2 min-w-0 shrink-0 truncate font-semibold">{p.skill.name}</b>
        {p.skipped ? <Badge title={t.skippedTitle}>{t.skipped}</Badge> : <Badge tone="update">{t.changed}</Badge>}
        {riskSummary(p.risks ?? []) && (
          <Badge tone="warn" className="min-w-0 shrink" title={riskSummary(p.risks ?? [])}>
            <span className="truncate">{riskSummary(p.risks ?? [])}</span>
          </Badge>
        )}
        {p.edited && (
          <span className="update-edited min-w-0 truncate text-caption text-muted-foreground" title={t.editedTitle}>
            {t.edited}
          </span>
        )}
      </label>
      <Button variant="ghost" size="sm" aria-pressed={viewed} className={cn(viewed && "bg-fill text-foreground")} onClick={onView}>
        {viewed ? t.hideDiff : t.showDiff}
      </Button>
      {!p.skipped && (
        <Button variant="ghost" size="sm" disabled={locked} onClick={onSkip} title={t.skipTitle}>
          {t.skip}
        </Button>
      )}
    </ListRow>
  );
}

// RenameRow 是推测出的改名。还没回答时是一个问题：先说确认后的影响，「是同一个」是这一行的主操作，「不是」按一删一增处理(onReject);
// 回答「是」(勾上，按改名更新)之后换成陈述：旧名 → 新名、已确认是同一个，以及更新后实际会怎样，「撤销」退回问题。
// 看差异、跳过是轻按钮，和回答分开放在右边。
export function RenameRow({ p, yes, viewed, locked, onCheck, onReject, onView, onSkip }: RowActions & { p: PlannedSkill; yes: boolean; onReject: () => void }) {
  const t = useT().plan;
  const name = <b className="font-semibold">{p.skill.name}</b>;
  const to = <b className="font-semibold">{p.renamed_to}</b>;
  return (
    <ListRow selected={yes} className="update-rename flex-col items-stretch gap-1">
      <div className="flex min-w-0 items-start gap-1.5">
        <MarkIcon tone="rename" className="mt-[3px]" />
        <div className="min-w-0 grow">
          {yes ? t.renameConfirmed(name, to) : t.renameQuestion(name, to)}
          {p.skipped && <Badge className="ml-1.5">{t.skipped}</Badge>}
        </div>
      </div>
      <div className="pl-[18px] text-caption text-muted-foreground">
        {yes ? t.renameYes(p.renamed_to) : t.renameNo(p.renamed_to)}
        {p.edited && <span className="update-edited">{t.renameEdited}</span>}
      </div>
      <div className="flex items-center gap-1.5 pl-[18px]">
        {yes ? (
          <>
            <Badge tone="rename" className="update-rename-yes">{t.sameOne}</Badge>
            <Button variant="ghost" size="sm" disabled={locked} onClick={() => onCheck(false)}>
              {t.undo}
            </Button>
          </>
        ) : (
          <>
            <Button variant="accent" size="sm" disabled={locked} onClick={() => onCheck(true)}>
              {t.yes}
            </Button>
            <Button size="sm" disabled={locked} onClick={onReject}>
              {t.no}
            </Button>
          </>
        )}
        <span className="grow" />
        <Button variant="ghost" size="sm" aria-pressed={viewed} className={cn(viewed && "bg-fill text-foreground")} onClick={onView}>
          {viewed ? t.hideDiff : t.showDiff}
        </Button>
        {!p.skipped && (
          <Button variant="ghost" size="sm" disabled={locked} onClick={onSkip} title={t.skipTitle}>
            {t.skip}
          </Button>
        )}
      </div>
    </ListRow>
  );
}

// DeletedRow 是来源里已删除的技能：移除(移除后这一行不再列出)或继续保留(之后只留淡色标记)。
export function DeletedRow({ p, source, locked, onRemoved }: { p: PlannedSkill; source: string; locked: boolean; onRemoved: () => void }) {
  const t = useT().plan;
  const [kept, setKept] = useState(false);
  return (
    <ListRow className="update-deleted flex-col items-stretch gap-1">
      <div className="flex min-w-0 items-start gap-1.5">
        <MarkIcon tone="info" className="mt-[3px]" />
        <div className="min-w-0 grow">
          <b className="font-semibold">{p.skill.name}</b>
          <span className="ml-1.5 text-caption text-muted-foreground">{t.deletedHere(source)}</span>
        </div>
      </div>
      <div className="flex items-center gap-1.5 pl-[18px]">
        {kept || p.skill.kept_deleted ? (
          <Badge>{t.keptDeleted}</Badge>
        ) : (
          <>
            <Button
              size="sm"
              danger
              disabled={locked}
              onClick={async () => {
                if (await removeSkill(p.skill.id)) onRemoved();
              }}
            >
              {t.remove}
            </Button>
            <Button
              size="sm"
              disabled={locked}
              onClick={async () => {
                if (await keepDeleted(p.skill.id)) setKept(true);
              }}
            >
              {t.keep}
            </Button>
          </>
        )}
      </div>
    </ListRow>
  );
}
