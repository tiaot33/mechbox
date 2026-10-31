// 移除技能，以及来源里已删除的技能选"继续保留"。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, SkillScopeStatus, type SkillID, type SkillScopeInspection } from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { mergedScopeKey, mergeProjectPlaces, skillName, skillScopesOf } from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { runEach, showResults } from "@/components/app/results";

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
    choices: installed ? folderChoices(t.removeFromScopes(installed, lostEdits(places))) : undefined,
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


// removeSkills 从库里移除勾选的技能(底部操作栏的"移除技能"):和移除一个一样问一次已安装的文件夹删掉还是保留，
// 一批只问一次，写出一共会删几处、其中几处被改过。有失败的才列出结果。
export async function removeSkills(ids: SkillID[]) {
  const s = currentSnapshot();
  const places = s.skillScopes.filter((p) => ids.includes(p.skill_id));
  // 几个技能装在同一个作用域只算一个作用域;项目里共用一个目录的几个 Agent 是一个。
  const scopes = new Set(places.map((p) => mergedScopeKey(s, p.scope))).size;
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.removeSkillsTitle(ids.length),
    body: <Hint>{t.removeSkillsHint}</Hint>,
    choices: scopes ? folderChoices(t.removeFromScopes(scopes, lostEdits(places))) : undefined,
    confirm: t.remove,
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  const rows = await runEach(ids, (id) => LibraryService.RemoveSkill(id, { folders: policy }), (id) => skillName(s, id));
  if (rows.length) await showResults(t.removeSkillsTitle(ids.length), rows);
  else ui.toast(t.removedSkills(ids.length));
}

// folderChoices 是移除技能时已安装的文件夹删掉还是保留;removeDesc 写出删掉会涉及几处。
function folderChoices(removeDesc: string) {
  const t = messages().actions;
  return [
    { value: FolderPolicy.Remove, label: t.removeInstalled, desc: removeDesc },
    { value: FolderPolicy.Keep, label: t.keepInstalled, desc: t.keepInstalledDesc },
  ];
}

// keepDeleted 是对来源里已删除的技能选"继续保留":不再提醒，只在技能上留一个淡色标记。
export function keepDeleted(id: SkillID) {
  const s = currentSnapshot();
  return ui.run(() => LibraryService.KeepDeletedSkill(id), { success: messages().update.keptDeleted(skillName(s, id)) });
}
