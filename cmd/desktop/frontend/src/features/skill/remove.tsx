// 移除技能，以及来源里已删除的技能选"继续保留"。
// 选删掉已安装的文件夹会让别的技能找不到它提到的技能时，在这个选项的说明里写出是哪几个;选保留文件夹时不受影响。
// 在技能详情的同名技能里移除另一个时(removeTwin),还可以在它装着的几处装上留下的这个。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, type Scope, type SkillID } from "@bindings/internal/library";
import { lostEdits } from "@/lib/consent";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, type Snapshot } from "@/lib/library";
import { placeCount, placeNames } from "@/lib/places";
import { skillName } from "@/lib/scope";
import { skillScopesOf } from "@/lib/installs";
import { sourceLabel } from "@/lib/source";
import { twinReplaceScopes } from "@/features/skill/remove-rules";
import { ChoiceList, Modal, ui } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { runEach, showResults } from "@/components/app/results";
import { inspectedSnapshot, installReplacing, lostMentionLines, lostMentions } from "@/features/install/install";
import type { MissingMention } from "@bindings/internal/library";
import { lostAfterTwin, removedPlaces } from "./remove-rules";

// removeSkill 移除技能(技能详情里的「移除技能」)。先现查磁盘(inspectedSnapshot):快照里上一次巡检的结果可能落后，
// 会漏掉「改动会丢掉」;读不成时不移除。返回是否移除了。
export async function removeSkill(id: SkillID) {
  const s = await inspectedSnapshot({ skill_id: id });
  if (!s) return false;
  const places = skillScopesOf(s, id);
  const installed = placeCount(s, places);
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

// removeTwin 是在技能详情的同名技能里移除不要的那个(id),留下正在看的这个(keep)。和移除技能一样问已安装的文件夹删掉还是保留，
// 删掉时写出是哪几处、那几处不再有这个技能(被改过的照样写出改动会丢掉);还可以勾「在这几处装上留下的这个」,默认不勾：
// 装上以后那几处的 Agent 用的就换成了留下的这个，要用户看过自己勾上。保留文件夹时文件夹还在那里，装不上，不给这个选项。
// 先现查磁盘(inspectedSnapshot):快照里上一次巡检的结果可能落后，会漏掉「改动会丢掉」;读不成时不移除。
// 能装上的几处见 twinReplaceScopes。返回是否移除了。
export async function removeTwin(id: SkillID, keep: SkillID) {
  const s = await inspectedSnapshot({});
  const skill = s?.skills.find((x) => x.id === id);
  if (!s || !skill || !s.skills.some((x) => x.id === keep)) return false;
  const places = skillScopesOf(s, id);
  const replace = twinReplaceScopes(s, id, keep);
  const unfound = places.length ? await lostMentions(places) : [];
  const picked = await ui.open<{ policy: FolderPolicy; replace: boolean }>((close) => (
    <RemoveTwinDialog s={s} id={id} keep={keep} replace={replace} unfound={unfound} close={close} />
  ));
  if (!picked) return false;
  const t = messages().actions;
  const done = await ui.run(() => LibraryService.RemoveSkill(id, { folders: picked.policy }).then(() => true), {
    success: picked.replace ? undefined : t.removed,
    label: t.removeLabel(skill.name),
  });
  if (!done) return false;
  if (picked.replace) await installReplacing(keep, replace);
  return true;
}

function RemoveTwinDialog({
  s,
  id,
  keep,
  replace,
  unfound,
  close,
}: {
  s: Snapshot;
  id: SkillID;
  keep: SkillID;
  replace: Scope[];
  unfound: MissingMention[];
  close: (value?: { policy: FolderPolicy; replace: boolean }) => void;
}) {
  const t = useT();
  const a = t.actions;
  const skill = s.skills.find((x) => x.id === id)!;
  const kept = s.skills.find((x) => x.id === keep)!;
  const places = skillScopesOf(s, id);
  const wheres = placeNames(s, places);
  const replaceWheres = placeNames(s, replace);
  const [policy, setPolicy] = useState<string>(FolderPolicy.Remove);
  const [swap, setSwap] = useState(false);
  const removing = policy === FolderPolicy.Remove;
  const swapping = removing && swap && replace.length > 0;
  // 换装时，那几处提到这个名字的技能又找得到了(找到的是留下的这个),不再算会找不到的。
  const lost = swapping ? lostAfterTwin(s, unfound, skill.name, replace) : unfound;
  const choices = wheres.length ? folderChoices(a.removeTwinFolders(wheres, skill.name, lostEdits(places).length), lost) : undefined;
  return (
    <Modal
      title={a.removeTwinTitle(skill.name, sourceLabel(s, skill.source))}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="destructive" autoFocus onClick={() => close({ policy: removing ? FolderPolicy.Remove : FolderPolicy.Keep, replace: swapping })}>
            {a.remove}
          </Button>
        </>
      }
    >
      <Hint>{a.removeSkillHint}</Hint>
      {choices && <ChoiceList choices={choices} value={policy} onChange={setPolicy} />}
      {removing && replace.length > 0 && (
        <label className="replace-twin flex cursor-pointer items-start gap-2 px-2.5">
          <input type="checkbox" className="mt-[3px] shrink-0 accent-primary" checked={swap} onChange={(e) => setSwap(e.target.checked)} />
          <div>
            <b className="block font-semibold">{a.replaceTwin}</b>
            <span className="text-body text-muted-foreground">{a.replaceTwinDesc(kept.name, sourceLabel(s, kept.source), replaceWheres)}</span>
          </div>
        </label>
      )}
    </Modal>
  );
}

// removeSkills 从库里移除勾选的技能(底部操作栏的"移除技能"):和移除一个一样问一次已安装的文件夹删掉还是保留，
// 一批只问一次，写出一共会删几处、其中几处被改过(先现查磁盘，读不成时不移除)。有失败的才列出结果。
// 返回是否至少移除了一个：取消了、都失败了、读不成时是 false。
export async function removeSkills(ids: SkillID[]): Promise<boolean> {
  const s = await inspectedSnapshot({});
  if (!s) return false;
  // 几个技能装在同一个作用域只算一个作用域;项目里共用一个目录的几个 Agent 是一个。
  const { places, scopes } = removedPlaces(s, ids);
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
