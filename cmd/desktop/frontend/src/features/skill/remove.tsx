// 移除技能，以及来源里已删除的技能选"继续保留"。
// 选删掉已安装的文件夹会让别的技能找不到它提到的技能时，在这个选项的说明里写出是哪几个;选保留文件夹时不受影响。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, type SkillID } from "@bindings/internal/library";
import { lostEdits } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { mergedScopeKey, mergeProjectPlaces, skillName, skillScopesOf } from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { runEach, showResults } from "@/components/app/results";
import { lostMentionLines, lostMentions } from "@/features/install/install";
import type { MissingMention } from "@bindings/internal/library";

export async function removeSkill(id: SkillID) {
  const s = currentSnapshot();
  const places = skillScopesOf(s, id);
  const installed = mergeProjectPlaces(s, places).length;
  const t = messages().actions;
  const unfound = await lostMentions(places);
  const choice = await ui.confirm({
    title: t.removeSkillTitle(skillName(s, id)),
    body: <Hint>{t.removeSkillHint}</Hint>,
    choices: installed ? folderChoices(t.removeFromScopes(installed, lostEdits(places).length), unfound) : undefined,
    confirm: t.remove,
    danger: true,
  });
  if (!choice) return false;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  const done = await ui.run(() => LibraryService.RemoveSkill(id, { folders: policy }).then(() => true), {
    success: t.removed,
    label: t.removeLabel(skillName(s, id)),
  });
  return !!done;
}


// removeSkills 从库里移除勾选的技能(底部操作栏的"移除技能"):和移除一个一样问一次已安装的文件夹删掉还是保留，
// 一批只问一次，写出一共会删几处、其中几处被改过。有失败的才列出结果。返回是否至少移除了一个：取消了、都失败了时是 false。
export async function removeSkills(ids: SkillID[]): Promise<boolean> {
  const s = currentSnapshot();
  const places = s.skillScopes.filter((p) => ids.includes(p.skill_id));
  // 几个技能装在同一个作用域只算一个作用域;项目里共用一个目录的几个 Agent 是一个。
  const scopes = new Set(places.map((p) => mergedScopeKey(s, p.scope))).size;
  const t = messages().actions;
  const unfound = await lostMentions(places);
  const choice = await ui.confirm({
    title: t.removeSkillsTitle(ids.length),
    body: <Hint>{t.removeSkillsHint}</Hint>,
    choices: scopes ? folderChoices(t.removeFromScopes(scopes, lostEdits(places).length), unfound) : undefined,
    confirm: t.remove,
    danger: true,
  });
  if (!choice) return false;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  const rows = await runEach(ids, (id) => LibraryService.RemoveSkill(id, { folders: policy }), (id) => skillName(s, id));
  if (rows.length) await showResults(t.removeSkillsTitle(ids.length), rows);
  else ui.toast(t.removedSkills(ids.length));
  return rows.length < ids.length;
}

// folderChoices 是移除技能时已安装的文件夹删掉还是保留;removeDesc 写出删掉会涉及几处，lost 是删掉后会找不到它的技能。
function folderChoices(removeDesc: string, lost: MissingMention[]) {
  const t = messages().actions;
  const desc = lost.length ? t.removeLostMentions(removeDesc, lostMentionLines(currentSnapshot(), lost)) : removeDesc;
  return [
    { value: FolderPolicy.Remove, label: t.removeInstalled, desc },
    { value: FolderPolicy.Keep, label: t.keepInstalled, desc: t.keepInstalledDesc },
  ];
}

// keepDeleted 是对来源里已删除的技能选"继续保留":不再提醒，只在技能上留一个淡色标记。
export function keepDeleted(id: SkillID) {
  const s = currentSnapshot();
  return ui.run(() => LibraryService.KeepDeletedSkill(id), {
    success: messages().update.keptDeleted(skillName(s, id)),
    label: messages().update.keepLabel(skillName(s, id)),
  });
}
