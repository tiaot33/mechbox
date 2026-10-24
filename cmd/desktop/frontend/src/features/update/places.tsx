// 更新计划里"装出去的地方":勾选这次一起换成新版的地方;用户改过的、文件夹不在了的默认不勾并写明原因。
import { SkillScopeStatus, type SkillID, type SkillScopeInspection } from "@bindings/internal/library";
import { canInstall, placeKey } from "@/lib/consent";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentName, linkNote, mergeProjectPlaces, mergedScopeKey, scopeKey, scopeLabel, sharedWithNote } from "@/lib/model";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";

// Places 按地方列出勾选的技能装在哪里;同一处装了几个技能时各自一个勾选框，状态分开给。
export function Places({
  places,
  name,
  on,
  onToggle,
  modified,
}: {
  places: SkillScopeInspection[];
  name: (id: SkillID) => string;
  on: (p: SkillScopeInspection) => boolean;
  onToggle: (p: SkillScopeInspection) => void;
  modified?: string; // 用户改过的地方旁边的说明
}) {
  const s = useSnapshot();
  const t = useT().plan;
  if (!places.length) return <Hint>{t.notPlaced}</Hint>;
  // unchecked 是链着这一处、这次没勾的地方：换这一处之前，库先给它们换上独立的一份。
  const unchecked = (p: SkillScopeInspection) =>
    (p.linked_by ?? []).filter((scope) => {
      const other = places.find((x) => x.skill_id === p.skill_id && scopeKey(x.scope) === scopeKey(scope));
      return !other || !on(other);
    });
  // 项目里共用一个文件夹的几处合成一行;勾选本来就按文件夹记，一起换。
  const rows = new Map<string, SkillScopeInspection[]>();
  for (const p of mergeProjectPlaces(s, places)) rows.set(mergedScopeKey(s, p.scope), [...(rows.get(mergedScopeKey(s, p.scope)) ?? []), p]);
  return (
    <ListBox>
      {[...rows.entries()].map(([key, list]) => (
        <ListRow key={key} className="update-place items-start">
          <span className="w-[190px] shrink-0 truncate pt-px">{scopeLabel(s, list[0].scope)}</span>
          <div className="flex min-w-0 grow flex-col gap-0.5">
            {list.map((p) => (
              <label key={placeKey(p)} className={cn("flex items-center gap-1.5", canInstall(p) && "cursor-pointer")}>
                <input type="checkbox" className="shrink-0 accent-primary" checked={on(p)} disabled={!canInstall(p)} onChange={() => onToggle(p)} />
                <span className="whitespace-nowrap">{name(p.skill_id)}</span>
                {p.status === SkillScopeStatus.SkillScopeModified && <Badge tone="warn">{modified ?? t.modifiedHere}</Badge>}
                {p.status === SkillScopeStatus.SkillScopeRootMissing && <Badge tone="bad">{t.rootMissing}</Badge>}
                {p.status === SkillScopeStatus.SkillScopeMissing && <Badge tone="warn">{t.folderMissing}</Badge>}
                {sharedWithNote(p).length > 0 && (
                  <span className="text-[11px] text-muted-foreground">
                    {t.sharedFolder(sharedWithNote(p).map((code) => agentName(s, code)))}
                  </span>
                )}
                {p.link_target && on(p) && (
                  <span className="update-place-link min-w-0 text-[11px] break-all text-muted-foreground" title={p.link_target}>
                    {linkNote(s, p.link_target)}
                  </span>
                )}
                {on(p) && unchecked(p).length > 0 && (
                  <span className="update-place-kept min-w-0 text-[11px] text-muted-foreground">
                    {t.linkedHere(mergeProjectPlaces(s, unchecked(p).map((scope) => ({ scope }))).map((x) => scopeLabel(s, x.scope)))}
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
