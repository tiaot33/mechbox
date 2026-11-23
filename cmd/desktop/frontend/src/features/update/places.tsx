// 更新计划里"装出去的地方":勾选这次一起换成新版的地方;用户改过的、文件夹不在了的默认不勾并写明原因;
// 在 app 外已经换成了这一版的写明不用再换。
import { SkillScopeStatus, type SkillID, type SkillScopeInspection } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentName, scopeKey } from "@/lib/scope";
import { canInstall, editedHere, linkNote, placeKey, sourceLatestHere } from "@/lib/installs";
import { eachPlace, placeLabel, placeNames, mergedScopeKey, sharedWithNote } from "@/lib/places";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";

// Places 按地方列出勾选的技能装在哪里：每个地方一行，地方名下面是装在那里的技能，各自一个勾选框，状态分开给。
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
  // 合成一处的几个 Agent 共用的文件夹合成一行，勾选本来就按文件夹记，一起换;它们各自装在不同目录时是不同的文件夹，按读它的 Agent 分行，各自勾选。
  const rows = new Map<string, SkillScopeInspection[]>();
  for (const p of eachPlace(s, places)) {
    const key = `${mergedScopeKey(s, p.scope)}|${placeLabel(s, p)}`;
    rows.set(key, [...(rows.get(key) ?? []), p]);
  }
  return (
    <ListBox>
      {[...rows.entries()].map(([key, list]) => (
        // 地方写在上面，要换的技能紧接着列在它下面、和地方名左对齐：读起来连在一起，不隔着一大片空白去对应。
        <ListRow key={key} className="update-place flex-col items-stretch gap-0.5">
          <span className="min-w-0 truncate font-medium">{placeLabel(s, list[0])}</span>
          <div className="flex min-w-0 flex-col gap-0.5">
            {list.map((p) => (
              <label key={placeKey(p)} className={cn("flex flex-wrap items-center gap-x-1.5 gap-y-0.5", canInstall(p) && "cursor-pointer")}>
                <input type="checkbox" className="shrink-0 accent-primary" checked={on(p)} disabled={!canInstall(p)} onChange={() => onToggle(p)} />
                <span className="whitespace-nowrap">{name(p.skill_id)}</span>
                {sourceLatestHere(p) && <span className="update-place-latest text-caption text-muted-foreground">{t.alreadyLatest}</span>}
                {editedHere(p) && <Badge tone="warn">{modified ?? t.modifiedHere}</Badge>}
                {p.status === SkillScopeStatus.SkillScopeRootMissing && <Badge>{t.rootMissing}</Badge>}
                {p.status === SkillScopeStatus.SkillScopeMissing && <Badge tone="critical">{t.folderMissing}</Badge>}
                {sharedWithNote(s, p).length > 0 && (
                  <span className="text-caption text-muted-foreground">
                    {t.sharedFolder(sharedWithNote(s, p).map((code) => agentName(s, code)))}
                  </span>
                )}
                {p.link_target && on(p) && (
                  <span className="update-place-link min-w-0 text-caption break-all text-muted-foreground" title={p.link_target}>
                    {linkNote(s, p.link_target)}
                  </span>
                )}
                {on(p) && unchecked(p).length > 0 && (
                  <span className="update-place-kept min-w-0 text-caption text-muted-foreground">
                    {t.linkedHere(placeNames(s, unchecked(p)))}
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
