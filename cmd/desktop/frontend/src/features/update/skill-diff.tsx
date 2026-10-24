// 更新计划里一个技能的文件差异：左边列出改了的文件，右边逐行比较(SKILL.md 按文档、脚本按代码)。
import { useCallback, useMemo } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { PlannedSkill, UpdatePlan } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import type { LibraryError } from "@/lib/library";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { FileDiff } from "./file-diff";
import { riskLabel } from "./risks";

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
