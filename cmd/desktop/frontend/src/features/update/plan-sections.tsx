// 更新计划弹窗里的几段：风险、GitHub 的提交说明、来源里的新技能。
import { useState } from "react";
import type { CollectionID, PlannedSkill, UpdatePlan, UpdateRisk } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { notInterested, previewNewSkills } from "@/features/fetch/preview";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { isRiskNotice, riskItems, riskLabel } from "./risks";
import { agentName } from "@/lib/scope";
import { Summary } from "@/components/app/parts";

// Risks 是更新计划最先给的：新增或改动的脚本、新增的可执行文件、技能放宽了对 Agent 声明的权限，点一条看那个文件的差异。
// 「会自动调用它了」只是提示：写在风险下面，不带「注意」的图标;只有它时也照样说没有风险。
export function Risks({ risks, onView }: { risks: { p: PlannedSkill; r: UpdateRisk }[]; onView: (p: PlannedSkill, path: string) => void }) {
  const t = useT().plan;
  const alerts = risks.filter(({ r }) => !isRiskNotice(r));
  const notices = risks.filter(({ r }) => isRiskNotice(r));
  return (
    <>
      <div className={sectionClass}>{t.firstLook}</div>
      {alerts.length ? (
        <Notice alert className="flex flex-col gap-0.5">
          {alerts.map(({ p, r }) => (
            <RiskLine key={`${p.skill.id}|${r.kind}|${r.path}`} name={p.renamed_to ?? p.skill.name} risk={r} onClick={() => onView(p, r.path)} />
          ))}
        </Notice>
      ) : (
        <Hint>{t.noRisks}</Hint>
      )}
      {notices.length > 0 && (
        <Notice className="flex flex-col gap-0.5">
          {notices.map(({ p, r }) => (
            <RiskLine key={`${p.skill.id}|${r.kind}|${r.path}`} name={p.renamed_to ?? p.skill.name} risk={r} onClick={() => onView(p, r.path)} />
          ))}
        </Notice>
      )}
    </>
  );
}

// RiskLine 是一条风险：技能名(更新单个技能时不写)、是什么风险，后面是文件路径;技能对 Agent 声明的设置写新增或改动的那几项，悬停看全。
export function RiskLine({ name, risk, onClick }: { name?: string; risk: UpdateRisk; onClick: () => void }) {
  const s = useSnapshot();
  const items = riskItems(risk);
  return (
    <button
      type="button"
      className="update-risk flex min-w-0 items-center gap-2 text-left hover:underline"
      title={items.length ? `${risk.path}\n${items.join("\n")}` : undefined}
      onClick={onClick}
    >
      {name && <b className="shrink-0 font-semibold">{name}</b>}
      <span className="shrink-0">{riskLabel(risk, risk.agent ? agentName(s, risk.agent) : "")}</span>
      <span className="min-w-0 truncate font-mono text-caption">{items.length ? items.join(", ") : risk.path}</span>
    </button>
  );
}

// RiskList 是更新单个技能的几条风险(「用目录里的修改更新」):风险一块，提示级的另一块、不带「注意」的图标。
export function RiskList({ risks, onView }: { risks: UpdateRisk[]; onView: (path: string) => void }) {
  const alerts = risks.filter((r) => !isRiskNotice(r));
  const notices = risks.filter(isRiskNotice);
  return (
    <>
      {alerts.length > 0 && (
        <Notice alert className="flex flex-col gap-0.5">
          {alerts.map((r) => (
            <RiskLine key={`${r.kind}|${r.path}`} risk={r} onClick={() => onView(r.path)} />
          ))}
        </Notice>
      )}
      {notices.length > 0 && (
        <Notice className="flex flex-col gap-0.5">
          {notices.map((r) => (
            <RiskLine key={`${r.kind}|${r.path}`} risk={r} onClick={() => onView(r.path)} />
          ))}
        </Notice>
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
    <div className="update-new flex items-center gap-2 text-body text-muted-foreground">
      <span className="grow">{t.newSkills(source, names)}</span>
      <Button variant="link" disabled={locked} onClick={() => void previewNewSkills(collectionID)}>
        {t.goFetch}
      </Button>
      <Button
        variant="ghost" size="inline"
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

export const sectionClass = "mt-1 text-body font-semibold";

// Commits 是 GitHub 来源这段时间的提交说明，每条只显示第一行，最新的在前。
export function Commits({ plan }: { plan: UpdatePlan }) {
  const t = useT().plan;
  const [all, setAll] = useState(false);
  const commits = plan.commits ?? [];
  const list = all ? commits : commits.slice(0, 6);
  return (
    <details open className="update-commits">
      <Summary className={sectionClass}>{t.commits(commits.length)}</Summary>
      <ListBox className="mt-1.5">
        {list.map((c) => (
          <ListRow key={c.sha} compact title={c.message}>
            <span className="shrink-0 font-mono text-caption text-muted-foreground">{c.sha.slice(0, 7)}</span>
            <span className="min-w-0 grow truncate">{c.message.split("\n")[0]}</span>
            <span className="shrink-0 text-caption text-muted-foreground">
              {[c.author, c.time && formatDate(c.time)].filter(Boolean).join(" · ")}
            </span>
          </ListRow>
        ))}
        {!all && commits.length > list.length && (
          <ListRow compact>
            <Button variant="ghost" size="inline" onClick={() => setAll(true)}>
              {t.moreCommits(commits.length - list.length)}
            </Button>
          </ListRow>
        )}
      </ListBox>
    </details>
  );
}
