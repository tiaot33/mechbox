// 更新计划弹窗里技能列表的几种行：有变化的、推测出改名的、来源里已删除的。
import { useState } from "react";
import type { PlannedSkill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { keepDeleted, removeSkill } from "@/features/skill/remove";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListRow } from "@/components/ui/list";
import { riskSummary } from "./risks";

interface RowActions {
  viewed: boolean; // 这个技能的差异正展开着
  locked: boolean; // 有写操作进行中，其他写操作不能点
  onCheck: (on: boolean) => void;
  onView: () => void;
  onSkip: () => void;
}

// ChangedRow 是有变化的技能：勾选才更新;标出风险、跳过过的、更新会换掉用户修改的，可以看差异、跳过这次改动。
export function ChangedRow({ p, checked, viewed, locked, onCheck, onView, onSkip }: RowActions & { p: PlannedSkill; checked: boolean }) {
  const t = useT().plan;
  return (
    <ListRow compact selected={checked} className="update-skill">
      <label className="flex min-w-0 grow cursor-pointer items-center gap-1.5">
        <input type="checkbox" className="shrink-0 accent-primary" checked={checked} onChange={(e) => onCheck(e.target.checked)} />
        <b className="font-semibold whitespace-nowrap">{p.skill.name}</b>
        {p.skipped ? <Badge title={t.skippedTitle}>{t.skipped}</Badge> : <Badge tone="warn">{t.changed}</Badge>}
        {(p.risks?.length ?? 0) > 0 && <Badge tone="bad">{riskSummary(p.risks ?? [])}</Badge>}
        {p.edited && (
          <span className="update-edited min-w-0 truncate text-[11px] text-muted-foreground" title={t.editedTitle}>
            {t.edited}
          </span>
        )}
      </label>
      <Button variant="link" onClick={onView}>
        {viewed ? t.hideDiff : t.showDiff}
      </Button>
      {!p.skipped && (
        <Button variant="link" disabled={locked} onClick={onSkip} title={t.skipTitle}>
          {t.skip}
        </Button>
      )}
    </ListRow>
  );
}

// RenameRow 是推测出的改名：回答"是"即勾上、按改名更新，"不是"按一删一增处理(onReject)。
export function RenameRow({ p, yes, viewed, locked, onCheck, onReject, onView, onSkip }: RowActions & { p: PlannedSkill; yes: boolean; onReject: () => void }) {
  const t = useT().plan;
  return (
    <ListRow selected={yes} className="update-rename">
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
          <Button variant="link" disabled={locked} onClick={() => onCheck(false)}>
            {t.undo}
          </Button>
        </>
      ) : (
        <>
          <Button size="sm" disabled={locked} onClick={() => onCheck(true)}>
            {t.yes}
          </Button>
          <Button size="sm" disabled={locked} onClick={onReject}>
            {t.no}
          </Button>
        </>
      )}
      <Button variant="link" onClick={onView}>
        {viewed ? t.hideDiff : t.showDiff}
      </Button>
      {!p.skipped && (
        <Button variant="link" disabled={locked} onClick={onSkip}>
          {t.skip}
        </Button>
      )}
    </ListRow>
  );
}

// DeletedRow 是来源里已删除的技能：移除(移除后这一行不再列出)或继续保留(之后只留淡色标记)。
export function DeletedRow({ p, source, locked, onRemoved }: { p: PlannedSkill; source: string; locked: boolean; onRemoved: () => void }) {
  const t = useT().plan;
  const [kept, setKept] = useState(false);
  return (
    <ListRow className="update-deleted">
      <div className="min-w-0 grow">
        <b className="font-semibold">{p.skill.name}</b>
        <span className="ml-1.5 text-[11px] text-muted-foreground">{t.deletedHere(source)}</span>
      </div>
      {kept || p.skill.kept_deleted ? (
        <Badge>{t.keptDeleted}</Badge>
      ) : (
        <>
          <Button
            size="sm"
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
    </ListRow>
  );
}
