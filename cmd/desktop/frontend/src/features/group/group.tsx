// 组：新建、重命名、删除，加入或移出组;删组、移出组时可选一并卸载只靠这个组装着的技能。
import { LibraryService } from "@bindings/cmd/desktop";
import { GroupMemberOutcome, MemberRemovalPolicy, type GroupID, type SkillID, type SkillScopeInspection } from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot, type Snapshot } from "@/lib/library";
import { folderPlaces, groupName, scopeKey, skillName } from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { DetachNotice } from "@/components/app/link-notes";
import { memberRows, showResults } from "@/components/app/results";

export async function addSkillToGroup(groupID: GroupID, id: SkillID) {
  const s = currentSnapshot();
  const t = messages().actions;
  const results = await ui.run(() => LibraryService.AddSkillToGroup(groupID, id));
  if (!results) return;
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  if (skipped.length) {
    await showResults(t.addedToGroupTitle(skillName(s, id), groupName(s, groupID)), memberRows(s, results), t.addedToGroupPartly);
  } else {
    ui.toast(t.addedToGroup(skillName(s, id), groupName(s, groupID), results.length));
  }
}

export async function removeSkillFromGroup(groupID: GroupID, id: SkillID) {
  const s = currentSnapshot();
  const t = messages().actions;
  // 该组安装到的作用域上，这个技能可能只随本组安装;有组安装时让用户决定去留。
  const installed = s.groupInstalls.filter((a) => a.group_id === groupID);
  let policy = MemberRemovalPolicy.KeepAsDirectInstall;
  if (installed.length) {
    const choice = await ui.confirm({
      title: t.removeFromGroupTitle(skillName(s, id), groupName(s, groupID)),
      body: (
        <>
          <Hint>{t.removeFromGroupHint(installed.length)}</Hint>
          <DetachNotice items={detachedBy(folderPlaces(s, groupOnly(s, groupID, [id])))} when="delete" prefix={t.ifUninstallToo} />
        </>
      ),
      choices: [
        { value: MemberRemovalPolicy.KeepAsDirectInstall, label: t.keep, desc: t.keepDesc },
        { value: MemberRemovalPolicy.UninstallMember, label: t.uninstallToo, desc: t.uninstallTooDesc },
      ],
      confirm: t.removeFromGroup,
    });
    if (!choice) return;
    policy = choice as MemberRemovalPolicy;
  }
  await ui.run(() => LibraryService.RemoveSkillFromGroup(groupID, id, { if_no_other_install: policy }), {
    success: t.removedFromGroup(skillName(s, id), groupName(s, groupID)),
  });
}

export async function createGroup(withSkill?: SkillID) {
  const t = messages().actions;
  const name = await ui.prompt({ title: t.newGroup, label: t.newGroupLabel, confirm: t.create });
  if (!name) return;
  const group = await ui.run(() => LibraryService.CreateGroup(name), {
    success: (g) => (g.name !== name ? t.groupCreatedAs(g.name) : t.groupCreated(g.name)),
    reload: !withSkill,
  });
  if (group && withSkill) await ui.run(() => LibraryService.AddSkillToGroup(group.id, withSkill));
  return group;
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
  // 有组安装时，只随本组安装的技能由用户决定去留，与移出成员一致。
  const installs = s.groupInstalls.filter((a) => a.group_id === groupID).length;
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.deleteGroupTitle(groupName(s, groupID)),
    body: (
      <>
        <Hint>{t.deleteGroupHint(installs)}</Hint>
        <DetachNotice
          items={detachedBy(folderPlaces(s, groupOnly(s, groupID, s.groups.find((g) => g.id === groupID)?.members ?? [])))}
          when="delete"
          prefix={t.ifUninstallToo}
        />
      </>
    ),
    choices: installs
      ? [
          { value: MemberRemovalPolicy.KeepAsDirectInstall, label: t.keep, desc: t.keepDesc },
          { value: MemberRemovalPolicy.UninstallMember, label: t.uninstallToo, desc: t.uninstallTooDesc },
        ]
      : undefined,
    confirm: t.deleteGroup,
    danger: true,
  });
  if (!choice) return;
  const policy = installs ? (choice as MemberRemovalPolicy) : MemberRemovalPolicy.KeepAsDirectInstall;
  await ui.run(() => LibraryService.DeleteGroup(groupID, { if_no_other_install: policy }), { success: t.groupDeleted });
}


// groupOnly 是这个组在它装到的各作用域里，只靠它留着的成员(没有直接安装、也没有别的组安装):选了一并卸载时这些文件夹会删。
function groupOnly(s: Snapshot, groupID: GroupID, members: SkillID[]): SkillScopeInspection[] {
  const scopes = new Set(s.groupInstalls.filter((g) => g.group_id === groupID).map((g) => scopeKey(g.scope)));
  return s.skillScopes.filter(
    (p) => members.includes(p.skill_id) && scopes.has(scopeKey(p.scope)) && !p.direct && (p.groups ?? []).every((g) => g === groupID),
  );
}
