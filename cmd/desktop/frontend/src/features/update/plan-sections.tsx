// 更新计划弹窗里的几段：风险、GitHub 的提交说明、来源里的新技能。
import { useState } from "react";
import { type CollectionID, type PlannedSkill, type UpdatePlan, type UpdateRisk } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { cn } from "@/lib/utils";
import { notInterested, previewNewSkills } from "@/features/fetch/preview";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { riskLabel } from "./risks";

// Risks 是更新计划最先给的：新增或改动的脚本、新增的可执行文件，点一条看那个文件的差异。
export function Risks({ risks, onView }: { risks: { p: PlannedSkill; r: UpdateRisk }[]; onView: (p: PlannedSkill, path: string) => void }) {
  const t = useT().plan;
  return (
    <>
      <div className={sectionClass}>{t.firstLook}</div>
      {risks.length ? (
        <Notice tone="warn" className="flex flex-col gap-0.5">
          {risks.map(({ p, r }) => (
            <button
              key={`${p.skill.id}|${r.path}`}
              type="button"
              className="update-risk flex items-center gap-2 text-left hover:underline"
              onClick={() => onView(p, r.path)}
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
  );
}

// NewSkills 是来源里的新技能，顺带一提："去入库"打开入库预览，"不感兴趣"之后不再提。
// 新技能按库里现在的集合说：生成计划、回答"不是"、入库或"不感兴趣"都经 run,完成后快照重读，这里跟着变;
// 点了"不感兴趣"的不等快照重读完，立即不再提。
export function NewSkills({ collectionID, source, locked }: { collectionID: CollectionID; source: string; locked: boolean }) {
  const s = useSnapshot();
  const t = useT().drawer;
  const [dismissed, setDismissed] = useState<string[]>([]);
  const names = (s.collections.find((c) => c.id === collectionID)?.new_skills ?? []).filter((name) => !dismissed.includes(name));
  if (!names.length) return null;
  return (
    <div className="update-new flex items-center gap-2 text-[12px] text-muted-foreground">
      <span className="grow">{t.newSkills(source, names)}</span>
      <Button variant="link" disabled={locked} onClick={() => void previewNewSkills(collectionID)}>
        {t.goFetch}
      </Button>
      <Button
        variant="link"
        disabled={locked}
        onClick={async () => {
          if (await notInterested(collectionID, names)) setDismissed((list) => [...list, ...names]);
        }}
      >
        {t.notInterested}
      </Button>
    </div>
  );
}

export const sectionClass = "mt-1 text-[13.5px] font-semibold";

// Commits 是 GitHub 来源这段时间的提交说明，每条只显示第一行，最新的在前。
export function Commits({ plan }: { plan: UpdatePlan }) {
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
