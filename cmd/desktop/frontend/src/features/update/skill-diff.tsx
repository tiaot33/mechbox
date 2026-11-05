// 更新计划里一个技能的文件差异：上面列出改了的文件，下面是选中文件的逐行比较(左右对比或行内对比)。
import { useCallback, useMemo } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { PlannedSkill, UpdatePlan, VersionDiff } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import type { LibraryError } from "@/lib/library";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ChangedFiles, changedFiles, FileDiff } from "./file-diff";
import { riskLabel } from "./risks";
import { openCompare } from "./version-compare";

// SkillDiff 是一个技能这次改了哪些文件，点文件看内容差异:库里一侧读当前版本，来源一侧读更新计划。
// 读来源一侧时计划已失效(超时或来源变了)由 onError 交回弹窗，提示重新生成。"放大查看"在铺满窗口的比较弹窗里看同样的内容。
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
  const t = useT().plan;
  const diff = item.diff;
  const files = changedFiles(diff);
  const kind = files.find((f) => f.path === path)?.kind;
  const versionID = item.skill.version_id;
  const before = useCallback(() => LibraryService.GetVersionFile(versionID, path), [versionID, path]);
  const after = useCallback(() => LibraryService.GetUpdatePlanFile(plan.id, item.skill.id, path), [plan.id, item.skill.id, path]);
  const risk = useMemo(() => new Map((item.risks ?? []).map((r) => [r.path, riskLabel(r)])), [item.risks]);
  const badge = (p: string) => risk.has(p) && <Badge tone="bad">{risk.get(p)}</Badge>;
  const title = t.changesOf(item.renamed_to ? `${item.skill.name} → ${item.renamed_to}` : item.skill.name);
  const enlarge = (diff: VersionDiff, origin: Element) =>
    openCompare({
      title,
      diff,
      before: (p) => LibraryService.GetVersionFile(versionID, p),
      after: (p) => LibraryService.GetUpdatePlanFile(plan.id, item.skill.id, p),
      path,
      badge,
      onError,
      origin,
    });
  return (
    <div className="update-diff flex flex-col gap-1.5 rounded-box border border-border-subtle bg-white/60 p-2.5">
      <div className="flex items-center gap-2">
        <b className="grow text-[12px]">{title}</b>
        {diff && files.length > 0 && (
          <Button variant="link" onClick={(e) => void enlarge(diff, e.currentTarget)}>
            {t.enlarge}
          </Button>
        )}
      </div>
      <ChangedFiles files={files} path={path} onPath={onPath} badge={badge} />
      {kind && <FileDiff path={path} before={kind === "added" ? null : before} after={kind === "deleted" ? null : after} onError={onError} />}
    </div>
  );
}
