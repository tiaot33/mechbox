// 安装与卸载：作用域页的开关、详情抽屉、整组开关与拖放(技能、组、集合)都走这里，冲突处理只写一份。
// 规则来自技能库的契约(go doc ./internal/library):
// - 安装前先看安装计划：不是从库里装的同名文件夹可选覆盖;被另一个技能占用或作用域根目录不存在时只能说明原因。
// - 组安装逐条返回结果，被跳过的成员列出原因。
// - 卸载会删掉别处链接指着的文件夹时，库先给那几处换上独立的一份，确认前说出来。
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  GroupMemberOutcome,
  InstallPlanStatus,
  type CollectionID,
  type GroupID,
  type InstallPlan,
  type Scope,
  type Skill,
  type SkillID,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import { currentSnapshot, toLibraryError, type Snapshot } from "@/lib/library";
import {
  agentName,
  collectionSkills,
  folderKey,
  folderPlaces,
  groupName,
  placeOf,
  scopeKey,
  scopeLabel,
  sharedWithNote,
  shortPath,
  skillName,
  skillScopeIn,
  sourceLabel,
} from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { lostEdits } from "@/features/skill/remove";
import { DetachNotice, SharedFolders } from "@/components/app/link-notes";
import { memberRows, showResults, summarizeMembers, type ResultRow } from "@/components/app/results";

export async function installSkill(id: SkillID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  const plan = await ui.run(() => LibraryService.PlanInstall(id, scope), { reload: false });
  if (!plan) return;
  const where = scopeLabel(s, scope);
  let overwrite = false;
  switch (plan.status) {
    case InstallPlanStatus.InstallOccupied:
      ui.toast(occupiedReason(s, id, plan), "error");
      return;
    case InstallPlanStatus.InstallUntracked: {
      const ok = await ui.confirm({
        title: t.untrackedTitle,
        body: <Notice tone="warn">{t.untrackedBody(shortPath(s, plan.folder), plan.name)}</Notice>,
        confirm: t.overwriteInstall,
        danger: true,
      });
      if (!ok) return;
      overwrite = true;
      break;
    }
  }
  await ui.run(() => LibraryService.Install(id, scope, { overwrite }), {
    success: t.installed(plan.name, where),
  });
}

// occupiedReason 说明为什么装不上：同名的另一个技能(或这个技能的另一个作用域)已装在那个文件夹;用来源区分是哪一个。
function occupiedReason(s: Snapshot, id: SkillID, plan: InstallPlan): string {
  const t = messages().actions;
  const occupier = s.skills.find((x) => x.id === plan.occupier_skill_id);
  const folder = shortPath(s, plan.folder);
  return occupier?.id === id
    ? t.occupiedSelf(folder)
    : occupier
      ? t.occupiedSource(sourceLabel(s, occupier.source), folder)
      : t.occupiedOther(folder);
}

// installCollection 是把集合拖到作用域：集合里还没直接安装在这里的技能逐个直接安装。一次性的，不像组安装那样记下来，
// 之后入库的新技能不会自动装过去。技能库没有整个集合的安装，先逐个看安装计划：被别的技能占用的跳过并说明原因;
// 有不是库装的同名文件夹的合成一次询问(跳过或覆盖),不逐个弹。有没装上的才列出结果，否则只提示装了几个。
export async function installCollection(id: CollectionID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  const collection = s.collections.find((c) => c.id === id);
  if (!collection) return;
  const label = sourceLabel(s, collection.source);
  const where = scopeLabel(s, scope);
  const skills = collectionSkills(s, collection).filter((x) => !skillScopeIn(s, x.id, scopeKey(scope))?.direct);
  await installEach(skills, scope, t.collectionInstallTitle(label, where), (n) => t.collectionInstalled(label, where, n));
}

// installSkills 把勾选的技能装到 scope(底部操作栏的"安装到…"、拖勾选着的一行到作用域):和集合一样逐个直接安装，
// 已经直接安装在这里的不再装;都已装在这里时只提示。
export async function installSkills(ids: SkillID[], scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  const where = scopeLabel(s, scope);
  const skills = s.skills.filter((x) => ids.includes(x.id) && !skillScopeIn(s, x.id, scopeKey(scope))?.direct);
  if (!skills.length) {
    ui.toast(t.pickedAllThere(where));
    return;
  }
  await installEach(skills, scope, t.pickedInstallTitle(skills.length, where), (n) => t.pickedInstalled(n, where));
}

// installEach 逐个直接安装：先逐个看安装计划，被别的技能占用的跳过并说明原因;有不是库装的同名文件夹的合成一次询问
// (跳过或覆盖),不逐个弹。有没装上的才列出结果，否则只提示装了几个(done)。
async function installEach(skills: Skill[], scope: Scope, title: string, done: (n: number) => string) {
  const s = currentSnapshot();
  const t = messages().actions;
  if (!skills.length) return;
  const plans = await ui.run(() => Promise.all(skills.map((x) => LibraryService.PlanInstall(x.id, scope))), { reload: false });
  if (!plans) return;
  const items = skills.map((skill, i) => ({ skill, plan: plans[i] }));
  const untracked = items.filter((x) => x.plan.status === InstallPlanStatus.InstallUntracked);
  let overwrite = false;
  if (untracked.length) {
    const choice = await ui.confirm({
      title,
      body: <Notice tone="warn">{t.untrackedSkills(untracked.length, untracked.map((x) => x.plan.name))}</Notice>,
      choices: [
        { value: "skip", label: t.skipSkills, desc: t.skipSkillsDesc },
        { value: "overwrite", label: t.overwriteFolders, desc: t.overwriteFoldersDesc },
      ],
      confirm: t.install,
    });
    if (!choice) return;
    overwrite = choice === "overwrite";
  }
  const rows: ResultRow[] = [];
  await ui.run(async () => {
    for (const { skill, plan } of items) {
      const isUntracked = plan.status === InstallPlanStatus.InstallUntracked;
      if (plan.status === InstallPlanStatus.InstallOccupied) {
        rows.push({ label: skill.name, outcome: t.memberSkipped, tone: "warn", reason: occupiedReason(s, skill.id, plan) });
        continue;
      }
      if (isUntracked && !overwrite) {
        rows.push({ label: skill.name, outcome: t.memberSkipped, tone: "warn", reason: t.untrackedBody(shortPath(s, plan.folder), plan.name) });
        continue;
      }
      try {
        await LibraryService.Install(skill.id, scope, { overwrite: isUntracked });
        rows.push({ label: skill.name, outcome: t.memberInstalled, tone: "ok" });
      } catch (e) {
        rows.push({ label: skill.name, outcome: t.failed, tone: "bad", reason: describeError(toLibraryError(e)) });
      }
    }
  }, { failed: () => rows.some((r) => r.tone === "bad") });
  const installed = rows.filter((r) => r.tone === "ok").length;
  if (rows.some((r) => r.tone !== "ok")) {
    await showResults(title, rows, t.installedSummary(installed, rows.length - installed));
  } else {
    ui.toast(done(installed));
  }
}

export async function uninstallSkill(id: SkillID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  // 全局共用目录：从其中一个卸载会撤销共用的各个 Agent 的直接安装，文件夹随之删除，先说明还有哪些 Agent 在用。
  // 项目里共用目录的几个 Agent 在界面上本来就是一处、名字里写着它们，不再另说。
  // 别处的符号链接指着这个文件夹时，删之前库先给那几处换上独立的一份，也先说明。
  const place = skillScopeIn(s, id, scopeKey(scope));
  const shared = place ? sharedWithNote(place) : [];
  const detached = detachedBy(place?.direct && !place.groups?.length ? folderPlaces(s, [place]) : []);
  if (place && (shared.length || detached.length)) {
    const ok = await ui.confirm({
      title: t.uninstallTitle(skillName(s, id)),
      body: (
        <>
          {shared.length > 0 && (
            <Notice tone="warn">
              {t.uninstallShared(
                shortPath(s, place.folder),
                shared.map((code) => agentName(s, code)),
                !!place.groups?.length,
              )}
            </Notice>
          )}
          <DetachNotice items={detached} when="delete" />
        </>
      ),
      confirm: t.uninstall,
      danger: true,
    });
    if (!ok) return;
  }
  await ui.run(() => LibraryService.Uninstall(id, scope), {
    success: t.uninstalled(skillName(s, id), scopeLabel(s, scope)),
    onError: (err) => {
      if (err.code !== ErrorCode.FailedPrecondition) return false;
      ui.toast(t.onlyViaGroup, "error");
      return true;
    },
  });
}


// uninstallSkills 从 scopes 卸载勾选的技能(底部操作栏的"从这里卸载",项目页是项目里的各个 Agent):撤销它们的直接安装。
// 一批卸载先确认一次：会删掉几个文件夹、其中几处被改过，全局共用的文件夹还有哪些 Agent 在用，别处链接指着的先换上独立的一份。
// 只随组安装的跳过并说明;有跳过或失败的才列出结果。
export async function uninstallSkills(ids: SkillID[], scopes: Scope[], where: string) {
  const s = currentSnapshot();
  const t = messages().actions;
  const places = scopes.flatMap((sc) => ids.map((id) => skillScopeIn(s, id, scopeKey(sc))).filter((p): p is SkillScopeInspection => !!p));
  const direct = places.filter((p) => p.direct);
  const viaGroup = places.filter((p) => !p.direct);
  if (!direct.length) {
    ui.toast(t.pickedNothingDirect(where), "error");
    return;
  }
  // 还有组安装留着的只撤直接安装，文件夹不删。
  const deleted = folderPlaces(s, direct.filter((p) => !p.groups?.length));
  // 全局共用的文件夹：只说会被删掉的那些(没有组安装留着);还有组安装留着的文件夹不删，各 Agent 照样算已装。
  const shared = direct.filter((p) => sharedWithNote(p).length && !p.groups?.length);
  const title = t.uninstallPickedTitle(new Set(direct.map((p) => p.skill_id)).size, where);
  const ok = await ui.confirm({
    title,
    body: (
      <>
        <Hint>{t.uninstallPickedHint(new Set(deleted.map(folderKey)).size, lostEdits(deleted), viaGroup.length)}</Hint>
        {shared.length > 0 && (
          <>
            <Notice tone="warn">{t.uninstallPickedShared}</Notice>
            <SharedFolders places={shared} />
          </>
        )}
        <DetachNotice items={detachedBy(deleted)} when="delete" />
      </>
    ),
    confirm: t.uninstall,
    danger: true,
  });
  if (!ok) return;
  const rows: ResultRow[] = viaGroup.map((p) => ({
    label: `${skillName(s, p.skill_id)} · ${scopeLabel(s, p.scope)}`,
    outcome: t.memberSkipped,
    tone: "warn",
    reason: t.onlyViaGroup,
  }));
  let done = 0;
  await ui.run(async () => {
    for (const p of direct) {
      try {
        await LibraryService.Uninstall(p.skill_id, p.scope);
        done++;
      } catch (e) {
        const err = toLibraryError(e);
        // 全局共用一个文件夹的几处：卸载其中一处时其余几处的直接安装一起撤了。
        if (err.code === ErrorCode.NotFound) continue;
        rows.push({ label: `${skillName(s, p.skill_id)} · ${scopeLabel(s, p.scope)}`, outcome: t.failed, tone: "bad", reason: describeError(err) });
      }
    }
  }, { failed: () => rows.some((r) => r.tone === "bad") });
  if (rows.length) await showResults(title, rows, t.uninstallPickedSummary(done, rows.length));
  else ui.toast(t.uninstalledPicked(done, where));
}

export async function installGroup(groupID: GroupID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  const plan = await ui.run(() => LibraryService.PlanGroupInstall(groupID, scope), { reload: false });
  if (!plan) return;
  const members = plan.members ?? [];
  const untracked = members.filter((m) => m.status === InstallPlanStatus.InstallUntracked);
  const blocked = members.filter(
    (m) => m.status === InstallPlanStatus.InstallOccupied || m.status === InstallPlanStatus.InstallDuplicate,
  );
  let overwrite = false;
  if (untracked.length) {
    const choice = await ui.confirm({
      title: t.groupInstallTitle(groupName(s, groupID), scopeLabel(s, scope)),
      body: (
        <Notice tone="warn">
          {t.groupUntracked(
            untracked.length,
            untracked.map((m) => m.name),
          )}
        </Notice>
      ),
      choices: [
        { value: "skip", label: t.skipMembers, desc: t.skipMembersDesc },
        { value: "overwrite", label: t.overwriteFolders, desc: t.overwriteFoldersDesc },
      ],
      confirm: t.install,
    });
    if (!choice) return;
    overwrite = choice === "overwrite";
  }
  const results = await ui.run(() => LibraryService.InstallGroup(groupID, scope, { overwrite }));
  if (!results) return;
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  if (skipped.length || blocked.length) {
    await showResults(t.groupInstallResults(groupName(s, groupID)), memberRows(s, results), summarizeMembers(results));
  } else {
    ui.toast(t.groupInstalled(groupName(s, groupID), scopeLabel(s, scope), summarizeMembers(results)));
  }
}

export async function uninstallGroup(groupID: GroupID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  // 共用目录：只靠这个组安装留着的成员，卸载后文件夹删除，共用的几个 Agent 都不再算已装，先说明还有哪些 Agent 在用。
  const memberOf = (id: GroupID, skill: SkillID) => !!s.groups.find((g) => g.id === id)?.members?.includes(skill);
  // 别处的符号链接指着要删的文件夹时，删之前库先给那几处换上独立的一份，也先说明。
  const gone = (s.groups.find((g) => g.id === groupID)?.members ?? [])
    .map((id) => skillScopeIn(s, id, scopeKey(scope)))
    .filter((p): p is SkillScopeInspection => !!p && !p.direct)
    .filter((p) => {
      const readers = [scope.agent, ...(p.shared_with ?? [])];
      const others = s.groupInstalls.filter(
        (g) =>
          readers.includes(g.scope.agent) &&
          placeOf(g.scope) === placeOf(scope) &&
          !(g.group_id === groupID && g.scope.agent === scope.agent),
      );
      return !others.some((g) => memberOf(g.group_id, p.skill_id));
    });
  const removed = gone.filter((p) => sharedWithNote(p).length);
  const detached = detachedBy(folderPlaces(s, gone));
  if (removed.length || detached.length) {
    const ok = await ui.confirm({
      title: t.groupUninstallTitle(groupName(s, groupID), scopeLabel(s, scope)),
      body: (
        <>
          {removed.length > 0 && (
            <>
              <Notice tone="warn">{t.groupUninstallShared(removed.length)}</Notice>
              <SharedFolders places={removed} />
            </>
          )}
          <DetachNotice items={detached} when="delete" />
        </>
      ),
      confirm: t.uninstall,
      danger: true,
    });
    if (!ok) return;
  }
  await ui.run(() => LibraryService.UninstallGroup(groupID, scope), {
    success: t.groupUninstalled(groupName(s, groupID), scopeLabel(s, scope)),
  });
}

