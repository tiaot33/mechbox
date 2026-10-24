// 移除技能，以及来源里已删除的技能选"继续保留"。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, SkillScopeStatus, type SkillID, type SkillScopeInspection } from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { mergeProjectPlaces, skillName, skillScopesOf } from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";

// lostEdits 是删除这些文件夹时有几处的改动会丢掉(状态取自快照里的巡检;共用目录的几个 Agent 共用一个文件夹，只算一处)。
export const lostEdits = (places: SkillScopeInspection[]) =>
  new Set(places.filter((p) => p.status === SkillScopeStatus.SkillScopeModified).map((p) => p.folder)).size;

export async function removeSkill(id: SkillID) {
  const s = currentSnapshot();
  const places = skillScopesOf(s, id);
  const installed = mergeProjectPlaces(s, places).length;
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.removeSkillTitle(skillName(s, id)),
    body: <Hint>{t.removeSkillHint}</Hint>,
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: t.removeInstalled,
            desc: t.removeFromScopes(installed, lostEdits(places)),
          },
          { value: FolderPolicy.Keep, label: t.keepInstalled, desc: t.keepInstalledDesc },
        ]
      : undefined,
    confirm: t.remove,
    danger: true,
  });
  if (!choice) return false;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  const done = await ui.run(() => LibraryService.RemoveSkill(id, { folders: policy }).then(() => true), {
    success: t.removed,
  });
  return !!done;
}


// keepDeleted 是对来源里已删除的技能选"继续保留":不再提醒，只在技能上留一个淡色标记。
export function keepDeleted(id: SkillID) {
  const s = currentSnapshot();
  return ui.run(() => LibraryService.KeepDeletedSkill(id), { success: messages().update.keptDeleted(skillName(s, id)) });
}
