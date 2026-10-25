// 组：新建、重命名、删除，加入(一个技能或整个集合)或移出组;删组、移出组时可选一并卸载只靠这个组装着的技能。
import { LibraryService } from "@bindings/cmd/desktop";
import {
  GroupMemberOutcome,
  MemberRemovalPolicy,
  type CollectionID,
  type GroupID,
  type GroupMemberResult,
  type SkillID,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { describeError } from "@/lib/errors";
import { currentSnapshot, toLibraryError, type LibraryError, type Snapshot } from "@/lib/library";
import { folderPlaces, groupName, missingFromGroup, scopeKey, skillName, sourceLabel } from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { DetachNotice } from "@/components/app/link-notes";
import { memberRows, showResults, type ResultRow } from "@/components/app/results";

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
  const outcomes = await ui.run(async () => {
    const all: { id: SkillID; results: GroupMemberResult[]; error?: LibraryError }[] = [];
    for (const id of ids) {
      try {
        all.push({ id, results: (await LibraryService.AddSkillToGroup(groupID, id)) ?? [] });
      } catch (e) {
        all.push({ id, results: [], error: toLibraryError(e) });
      }
    }
    return all;
  });
  if (!outcomes) return;
  const added = outcomes.filter((o) => !o.error);
  const skipped = added.some((o) => o.results.some((r) => r.outcome === GroupMemberOutcome.MemberSkipped));
  if (added.length === ids.length && !skipped) {
    ui.toast(t.collectionAddedToGroup(label, ids.length, group, s.groupInstalls.filter((a) => a.group_id === groupID).length));
    return;
  }
  const rows = outcomes.flatMap((o): ResultRow[] => {
    if (o.error) return [{ label: skillName(s, o.id), outcome: t.failed, tone: "bad", reason: describeError(o.error) }];
    if (o.results.length) return memberRows(s, o.results);
    return [{ label: skillName(s, o.id), outcome: t.memberAdded, tone: "ok" }];
  });
  await showResults(t.collectionAddedToGroupTitle(label, group, added.length, ids.length), rows, skipped ? t.collectionAddedPartly : undefined);
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

// createGroup 新建组;withSkills 是建好后一并加入的技能(详情里的"新建组…"、左栏集合右键菜单的"加入组 → 新建组…")。
export async function createGroup(withSkills: SkillID[] = []) {
  const t = messages().actions;
  const name = await ui.prompt({ title: t.newGroup, label: t.newGroupLabel, confirm: t.create });
  if (!name) return;
  const group = await ui.run(() => LibraryService.CreateGroup(name), {
    success: (g) => (g.name !== name ? t.groupCreatedAs(g.name) : t.groupCreated(g.name)),
    reload: !withSkills.length,
  });
  if (group && withSkills.length) {
    await ui.run(async () => {
      for (const id of withSkills) await LibraryService.AddSkillToGroup(group.id, id);
    });
  }
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
