// 组：新建、重命名、删除，加入(一个技能、整个集合或勾选的几个技能)或移出组;删组、移出组时可选一并卸载只靠这个组装着的技能。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import { GroupMemberOutcome, MemberRemovalPolicy, type CollectionID, type GroupID, type SkillID } from "@bindings/internal/library";
import { discardedEdits } from "@/lib/consent";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, useSnapshot } from "@/lib/library";
import { folderPlaces } from "@/lib/installs";
import { groupName, skillName } from "@/lib/scope";
import { missingFromGroup } from "@/features/group/group-rules";
import { sourceLabel } from "@/lib/source";
import { Modal, ui } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { NativeSelect } from "@/components/ui/native-select";
import { DeleteNotes } from "@/components/app/link-notes";
import { runDiscardingEdits } from "@/components/app/discard-edits";
import { LostMentions, lostMentions, ReadsTwiceNotice, toastInstalled } from "@/features/install/install";
import { runEach, showResults } from "@/components/app/results";
import { memberRows, placedBy } from "@/components/app/result-rows";
import { addReadsTwice, addedToGroup, groupOnly, inGroup, notInGroup } from "./group-rules";

export async function addSkillToGroup(groupID: GroupID, id: SkillID) {
  const s = currentSnapshot();
  const t = messages().actions;
  if (!(await confirmReadsTwice(groupID, [id]))) return;
  const results = await ui.run(() => LibraryService.AddSkillToGroup(groupID, id));
  if (!results) return;
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  if (skipped.length) {
    await showResults(t.addedToGroupTitle(skillName(s, id), groupName(s, groupID)), memberRows(s, results, true), t.addedToGroupPartly);
  } else {
    await toastInstalled(placedBy(results), t.addedToGroup(skillName(s, id), groupName(s, groupID), results.length));
    return;
  }
  await toastInstalled(placedBy(results));
}

// addCollectionToGroup 把集合里还不在组里的技能逐个加入组(集合拖到组上、左栏集合的右键菜单)。每个技能各是一次写操作，
// 一个失败不影响其余的;有失败或有作用域没装上时逐个列出已加入、被跳过、失败的技能。
export async function addCollectionToGroup(groupID: GroupID, collectionID: CollectionID) {
  const s = currentSnapshot();
  const t = messages().actions;
  const c = s.collections.find((x) => x.id === collectionID);
  if (!c) return;
  const label = sourceLabel(s, c.source);
  const group = groupName(s, groupID);
  const ids = missingFromGroup(s, collectionID, groupID);
  if (!ids.length) {
    ui.toast(t.collectionAlreadyInGroup(label, group));
    return;
  }
  await addEachToGroup(groupID, ids, {
    title: (added, total) => t.collectionAddedToGroupTitle(label, group, added, total),
    done: (n, scopes) => t.collectionAddedToGroup(label, n, group, scopes),
  });
}

// addSkillsToGroup 把勾选的技能里还不在组里的加入组(底部操作栏的"加入组…"、拖勾选着的一行到组上);都已在组里时只提示。
// 返回是否至少加入了一个：都已在组里、都失败了时是 false。
export async function addSkillsToGroup(groupID: GroupID, picked: SkillID[]): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  const group = groupName(s, groupID);
  const ids = notInGroup(s, groupID, picked);
  if (!ids.length) {
    ui.toast(t.pickedAlreadyInGroup(group));
    return false;
  }
  return addEachToGroup(groupID, ids, {
    title: (added, total) => t.pickedAddedToGroupTitle(group, added, total),
    done: (n, scopes) => t.pickedAddedToGroup(n, group, scopes),
  });
}

// addEachToGroup 把技能逐个加入组：每个技能各是一次写操作，一个失败不影响其余的;有失败或有作用域没装上时
// 逐个列出已加入、被跳过、失败的技能，否则只提示加入了几个(done,scopes 是该组已组安装的作用域数)。返回是否至少加入了一个。
async function addEachToGroup(
  groupID: GroupID,
  ids: SkillID[],
  text: { title: (added: number, total: number) => string; done: (n: number, scopes: number) => string },
): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  if (!(await confirmReadsTwice(groupID, ids))) return false;
  const results = await runEach(ids, (id) => LibraryService.AddSkillToGroup(groupID, id).then((r) => r ?? []));
  if (!results) return false;
  const { all, skipped, added, placed, rows } = addedToGroup(s, ids, results);
  if (all) {
    await toastInstalled(placed, text.done(ids.length, s.groupInstalls.filter((a) => a.group_id === groupID).length));
    return true;
  }
  await showResults(text.title(added, ids.length), rows, skipped ? t.membersAddedPartly : undefined);
  await toastInstalled(placed);
  return added > 0;
}

// confirmReadsTwice 在把技能加入组之前，对这个组已组安装的各处取安装计划;有别的 Agent 会读到两份时先说明，用户同意再加入。
// 组还没有组安装、或没有会读到两份的时直接返回 true;取计划失败的那几处不说(加入后照常装，结果里会列出)。
async function confirmReadsTwice(groupID: GroupID, ids: SkillID[]): Promise<boolean> {
  const s = currentSnapshot();
  const scopes = s.groupInstalls.filter((a) => a.group_id === groupID).map((a) => a.scope);
  if (!scopes.length) return true;
  const plans = await ui.read(() =>
    Promise.all(
      ids.flatMap((id) => scopes.map((scope) => LibraryService.PlanInstall(id, scope).then((plan) => (plan ? [{ scope, plan }] : []), () => []))),
    ).then((all) => all.flat()),
  );
  if (!plans) return false;
  const notes = addReadsTwice(s, plans);
  if (!notes.length) return true;
  const t = messages().actions;
  return !!(await ui.confirm({ title: t.addToGroupReadsTwiceTitle(groupName(s, groupID)), body: <ReadsTwiceNotice notes={notes} />, confirm: t.addToGroupConfirm }));
}

export async function removeSkillFromGroup(groupID: GroupID, id: SkillID) {
  const s = currentSnapshot();
  const t = messages().actions;
  // 该组安装到的作用域上，这个技能可能只随本组安装;有组安装时让用户决定去留。
  // 选「一并卸载」会删掉的文件夹里哪几处被手动改过，按界面上已经读到的状态说;确认之后才改过的，删之前由技能库核对出来再问一次。
  const installed = s.groupInstalls.filter((a) => a.group_id === groupID);
  let policy = MemberRemovalPolicy.KeepAsDirectInstall;
  let discard: string[] = [];
  if (installed.length) {
    const gone = folderPlaces(s, groupOnly(s, groupID, [id]));
    const unfound = await lostMentions(gone);
    const choice = await ui.confirm({
      title: t.removeFromGroupTitle(skillName(s, id), groupName(s, groupID)),
      body: (
        <>
          <Hint>{t.removeFromGroupHint(installed.length)}</Hint>
          <DeleteNotes deleted={gone} prefix={t.ifUninstallToo} />
          <LostMentions lost={unfound} prefix={t.ifUninstallToo} />
        </>
      ),
      choices: removalChoices(),
      confirm: t.removeFromGroup,
    });
    if (!choice) return;
    policy = choice as MemberRemovalPolicy;
    discard = discardedEdits(gone);
  }
  await runDiscardingEdits(discard, (discard) => LibraryService.RemoveSkillFromGroup(groupID, id, { if_no_other_install: policy, discard_edits: discard }), {
    success: t.removedFromGroup(skillName(s, id), groupName(s, groupID)),
  });
}

// removeSkillsFromGroup 把勾选的技能里在组里的移出组(底部操作栏的"移出组…"):组有组安装时和移出一个成员一样，
// 先问只随本组安装的留着(改为直接安装)还是一并卸载，一批只问一次。一次移出，全成或全不成;
// 确认之后才被改过的，技能库删之前核对出来再问一次。返回是否移出了：都不在组里、取消了、失败了时是 false。
export async function removeSkillsFromGroup(groupID: GroupID, picked: SkillID[]): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  const group = groupName(s, groupID);
  const ids = inGroup(s, groupID, picked);
  if (!ids.length) return false;
  const installed = s.groupInstalls.filter((a) => a.group_id === groupID);
  // 选组的弹窗里已经点过"移出";没有组安装时不再问，和移出一个成员一样直接移出。
  let policy = MemberRemovalPolicy.KeepAsDirectInstall;
  let discard: string[] = [];
  if (installed.length) {
    const gone = folderPlaces(s, groupOnly(s, groupID, ids));
    const unfound = await lostMentions(gone);
    const choice = await ui.confirm({
      title: t.pickedRemoveFromGroupTitle(ids.length, group),
      body: (
        <>
          <Hint>{t.removeFromGroupHint(installed.length)}</Hint>
          <DeleteNotes deleted={gone} prefix={t.ifUninstallToo} />
          <LostMentions lost={unfound} prefix={t.ifUninstallToo} />
        </>
      ),
      choices: removalChoices(),
      confirm: t.removeFromGroup,
    });
    if (!choice) return false;
    policy = choice as MemberRemovalPolicy;
    discard = discardedEdits(gone);
  }
  const done = await runDiscardingEdits(
    discard,
    (discard) => LibraryService.RemoveSkillsFromGroup(groupID, ids, { if_no_other_install: policy, discard_edits: discard }).then(() => true),
    { success: t.pickedRemovedFromGroup(ids.length, group) },
  );
  return !!done;
}

// createGroup 新建组;withSkills 是建好后一并加入的技能(详情里的"新建组…"、左栏集合右键菜单的"加入组 → 新建组…")。
// 返回建好的组与加进去了几个技能;没建成时是 undefined。
export async function createGroup(withSkills: SkillID[] = []) {
  const t = messages().actions;
  const name = await ui.prompt({ title: t.newGroup, label: t.newGroupLabel, confirm: t.create });
  if (!name) return;
  const group = await ui.run(() => LibraryService.CreateGroup(name), {
    success: (g) => (g.name !== name ? t.groupCreatedAs(g.name) : t.groupCreated(g.name)),
    reload: !withSkills.length,
  });
  let added = 0;
  if (group && withSkills.length) {
    await ui.run(async () => {
      for (const id of withSkills) {
        await LibraryService.AddSkillToGroup(group.id, id);
        added++;
      }
    });
  }
  return group && { group, added };
}

export async function renameGroup(groupID: GroupID) {
  const s = currentSnapshot();
  const current = groupName(s, groupID);
  const t = messages().actions;
  const name = await ui.prompt({ title: t.renameGroup, initial: current, confirm: t.rename });
  if (!name || name === current) return;
  await ui.run(() => LibraryService.RenameGroup(groupID, name), {
    success: (g) => (g.name !== name ? t.groupRenamedAs(g.name) : t.groupRenamed(g.name)),
  });
}

export async function deleteGroup(groupID: GroupID) {
  const s = currentSnapshot();
  // 有组安装时，只随本组安装的技能由用户决定去留，与移出成员一致;选「一并卸载」会删掉的文件夹里哪几处被改过按界面上已经读到的状态说。
  const installs = s.groupInstalls.filter((a) => a.group_id === groupID).length;
  const t = messages().actions;
  const gone = folderPlaces(s, groupOnly(s, groupID, s.groups.find((g) => g.id === groupID)?.members ?? []));
  const unfound = await lostMentions(gone);
  const choice = await ui.confirm({
    title: t.deleteGroupTitle(groupName(s, groupID)),
    body: (
      <>
        <Hint>{t.deleteGroupHint(installs)}</Hint>
        <DeleteNotes deleted={gone} prefix={t.ifUninstallToo} />
        <LostMentions lost={unfound} prefix={t.ifUninstallToo} />
      </>
    ),
    choices: installs ? removalChoices() : undefined,
    confirm: t.deleteGroup,
    danger: true,
  });
  if (!choice) return;
  const policy = installs ? (choice as MemberRemovalPolicy) : MemberRemovalPolicy.KeepAsDirectInstall;
  await runDiscardingEdits(discardedEdits(gone), (discard) => LibraryService.DeleteGroup(groupID, { if_no_other_install: policy, discard_edits: discard }), {
    success: t.groupDeleted,
  });
}


// removalChoices 是移出成员、删除组时只随本组安装的技能怎么处理：保留(改为直接安装)或一并卸载。
function removalChoices() {
  const t = messages().actions;
  return [
    { value: MemberRemovalPolicy.KeepAsDirectInstall, label: t.keep, desc: t.keepDesc },
    { value: MemberRemovalPolicy.UninstallMember, label: t.uninstallToo, desc: t.uninstallTooDesc },
  ];
}

// pickGroup 是勾选后操作栏的"加入组…""移出组…":选一个组，把勾选的技能加入(也可以新建一个组)或移出。
// 返回是否至少加入、移出了一个(新建组时只建成了组、一个都没加进去也是 false);选组、给新组起名、确认时取消了是 false。
export async function pickGroup(ids: SkillID[], mode: "add" | "remove"): Promise<boolean> {
  const chosen = await ui.open<GroupID | "new">((close) => <GroupDialog ids={ids} mode={mode} close={close} />);
  if (chosen === undefined) return false;
  if (chosen === "new") return ((await createGroup(ids))?.added ?? 0) > 0;
  return mode === "add" ? addSkillsToGroup(chosen, ids) : removeSkillsFromGroup(chosen, ids);
}

// GroupDialog 选组：加入时列出全部组(勾选的都已在里面的选不了)和"新建组…";移出时只列有勾选的技能在里面的组。
function GroupDialog({ ids, mode, close }: { ids: SkillID[]; mode: "add" | "remove"; close: (group?: GroupID | "new") => void }) {
  const s = useSnapshot();
  const t = useT();
  const has = (members: SkillID[] | null | undefined, id: SkillID) => !!members?.includes(id);
  const groups = s.groups.filter((g) => mode === "add" || ids.some((id) => has(g.members, id)));
  const full = (members: SkillID[] | null | undefined) => ids.every((id) => has(members, id));
  const first = mode === "add" ? (groups.find((g) => !full(g.members))?.id ?? "new") : groups[0]?.id;
  const [value, setValue] = useState<GroupID | "new" | undefined>(first);
  return (
    <Modal
      title={mode === "add" ? t.list.pickAddTitle(ids.length) : t.list.pickRemoveTitle(ids.length)}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="primary" disabled={value === undefined} onClick={() => close(value)}>
            {mode === "add" ? (value === "new" ? t.list.pickNewGroupNext : t.list.pickAdd) : t.actions.removeFromGroup}
          </Button>
        </>
      }
    >
      <Field label={t.list.pickGroup} className="group-picked">
        <NativeSelect
          className="max-w-[260px]"
          value={value ?? ""}
          onChange={(e) => setValue(e.target.value === "new" ? "new" : Number(e.target.value))}
        >
          {groups.map((g) => (
            <option key={g.id} value={g.id} disabled={mode === "add" && full(g.members)}>
              {mode === "add" && full(g.members) ? t.list.pickAllInGroup(g.name) : g.name}
            </option>
          ))}
          {mode === "add" && <option value="new">{t.list.pickNewGroup}</option>}
        </NativeSelect>
      </Field>
    </Modal>
  );
}
