// 写操作的公共流程：卡片开关、详情抽屉、整组开关与拖放都走这里，冲突处理只写一份。
// 规则来自技能库的契约(go doc ./internal/library):
// - 安装前先看安装计划：不是从库里装的同名文件夹可选覆盖;被另一个技能占用或作用域根目录不存在时只能说明原因。
// - 重新安装先现查各处状态，只换不是当前版本的几处;被改过的逐处由用户勾选是否覆盖，文件夹不在了的逐处由用户选不动、装回去还是卸载。
// - 组安装 / 加入组 / 重新安装逐条返回结果，被跳过或失败的条目列出原因。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  FolderPolicy,
  GroupMemberOutcome,
  InstallPlanStatus,
  MemberRemovalPolicy,
  MigrationOutcome,
  ReinstallOutcome,
  SkillScopeStatus,
  type Agent,
  type CollectionID,
  type CustomAgent,
  type FetchUnmanagedResult,
  type GroupID,
  type GroupMemberResult,
  type RegisteredProject,
  type ReinstallRequest,
  type ReinstallResult,
  type Scope,
  type SkillID,
  type SkillScopeFilter,
  type SkillScopeInspection,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import { describeError, unmanagedSourceError } from "./errors";
import { messages, useT } from "./i18n";
import { foundSource, toLibraryError, useFoundSources, type LibraryError, type Snapshot } from "./library";
import { FetchReview } from "@/components/app/fetch-review";
import { offerProject } from "@/components/app/fetch-unmanaged-dialog";
import {
  agentName,
  deletedInSource,
  folderKey,
  installedWhere,
  linkNote,
  outsideLinkNote,
  needsReinstall,
  noSource,
  pending,
  placeOf,
  projectName,
  scopeKey,
  scopeLabel,
  shortPath,
  skillScopeIn,
  skillScopesOf,
  renamedTo,
  sameNameFetchNote,
  sourceLabel,
  updateAvailable,
} from "./model";
import { Modal, useUI } from "./ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ListBox, ListRow } from "@/components/ui/list";
import { Segmented } from "@/components/ui/segmented";
import { Notice } from "@/components/ui/notice";

type UI = ReturnType<typeof useUI>;

const skillName = (s: Snapshot, id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;
const groupName = (s: Snapshot, id: GroupID) => s.groups.find((g) => g.id === id)?.name ?? `#${id}`;

// ---------- 结果列表 ----------

interface ResultRow {
  label: string;
  outcome: string;
  tone: "ok" | "warn" | "bad";
  reason?: string;
}

function showResults(ui: UI, title: string, rows: ResultRow[], note?: string) {
  const t = messages().actions;
  return ui.open((close) => (
    <Modal
      title={title}
      onClose={() => close()}
      footer={
        <Button variant="primary" onClick={() => close()}>
          {t.gotIt}
        </Button>
      }
    >
      {note && <Notice>{note}</Notice>}
      <ListBox>
        {rows.map((r, i) => (
          <ListRow key={i}>
            <div className="min-w-0 grow">
              <div>{r.label}</div>
              {r.reason && <div className="text-xs whitespace-pre-line text-muted-foreground">{r.reason}</div>}
            </div>
            <Badge tone={r.tone}>{r.outcome}</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  ));
}

function memberOutcome(outcome: string): { text: string; tone: ResultRow["tone"] } | undefined {
  const t = messages().actions;
  return {
    [GroupMemberOutcome.MemberInstalled]: { text: t.memberInstalled, tone: "ok" as const },
    [GroupMemberOutcome.MemberAlreadyInstalled]: { text: t.memberAlready, tone: "ok" as const },
    [GroupMemberOutcome.MemberSkipped]: { text: t.memberSkipped, tone: "warn" as const },
  }[outcome];
}

function memberRows(s: Snapshot, results: GroupMemberResult[]): ResultRow[] {
  return results.map((r) => ({
    label: `${skillName(s, r.skill_id)} → ${scopeLabel(s, r.scope)}`,
    outcome: memberOutcome(r.outcome)?.text ?? r.outcome,
    tone: memberOutcome(r.outcome)?.tone ?? "warn",
    reason: r.error ? describeError(r.error) : undefined,
  }));
}

function summarizeMembers(results: GroupMemberResult[]): string {
  const count = (o: GroupMemberOutcome) => results.filter((r) => r.outcome === o).length;
  return messages().actions.membersSummary(
    count(GroupMemberOutcome.MemberInstalled),
    count(GroupMemberOutcome.MemberAlreadyInstalled),
    count(GroupMemberOutcome.MemberSkipped),
  );
}

// ---------- 单个技能的安装 ----------

export async function installSkill(ui: UI, s: Snapshot, id: SkillID, scope: Scope) {
  const t = messages().actions;
  const plan = await ui.run(() => LibraryService.PlanInstall(id, scope), { reload: false });
  if (!plan) return;
  const where = scopeLabel(s, scope);
  let overwrite = false;
  switch (plan.status) {
    case InstallPlanStatus.InstallOccupied: {
      // 占用者与本技能同名;用来源区分是哪一个。
      const occupier = s.skills.find((x) => x.id === plan.occupier_skill_id);
      const folder = shortPath(s, plan.folder);
      ui.toast(
        occupier?.id === id
          ? t.occupiedSelf(folder)
          : occupier
            ? t.occupiedSource(sourceLabel(s, occupier.source), folder)
            : t.occupiedOther(folder),
        "error",
      );
      return;
    }
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

export async function uninstallSkill(ui: UI, s: Snapshot, id: SkillID, scope: Scope) {
  const t = messages().actions;
  // 共用目录：从其中一个卸载会撤销共用的各个 Agent 的直接安装，文件夹随之删除，先说明还有哪些 Agent 在用。
  // 别处的符号链接指着这个文件夹时，删之前库先给那几处换上独立的一份，也先说明。
  const place = skillScopeIn(s, id, scopeKey(scope));
  const shared = place?.shared_with ?? [];
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
          <DetachNotice s={s} items={detached} when="delete" />
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

// ---------- 组安装 ----------

export async function installGroup(ui: UI, s: Snapshot, groupID: GroupID, scope: Scope) {
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
    await showResults(ui, t.groupInstallResults(groupName(s, groupID)), memberRows(s, results), summarizeMembers(results));
  } else {
    ui.toast(t.groupInstalled(groupName(s, groupID), scopeLabel(s, scope), summarizeMembers(results)));
  }
}

export async function uninstallGroup(ui: UI, s: Snapshot, groupID: GroupID, scope: Scope) {
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
  const removed = gone.filter((p) => p.shared_with?.length);
  const detached = detachedBy(folderPlaces(s, gone));
  if (removed.length || detached.length) {
    const ok = await ui.confirm({
      title: t.groupUninstallTitle(groupName(s, groupID), scopeLabel(s, scope)),
      body: (
        <>
          {removed.length > 0 && (
            <>
              <Notice tone="warn">{t.groupUninstallShared(removed.length)}</Notice>
              <SharedFolders s={s} places={removed} />
            </>
          )}
          <DetachNotice s={s} items={detached} when="delete" />
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

// ---------- 组成员与组本身 ----------

export async function addSkillToGroup(ui: UI, s: Snapshot, groupID: GroupID, id: SkillID) {
  const t = messages().actions;
  const results = await ui.run(() => LibraryService.AddSkillToGroup(groupID, id));
  if (!results) return;
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  if (skipped.length) {
    await showResults(
      ui,
      t.addedToGroupTitle(skillName(s, id), groupName(s, groupID)),
      memberRows(s, results),
      t.addedToGroupPartly,
    );
  } else {
    ui.toast(t.addedToGroup(skillName(s, id), groupName(s, groupID), results.length));
  }
}

export async function removeSkillFromGroup(ui: UI, s: Snapshot, groupID: GroupID, id: SkillID) {
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
          <DetachNotice s={s} items={detachedBy(folderPlaces(s, groupOnly(s, groupID, [id])))} when="delete" prefix={t.ifUninstallToo} />
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

export async function createGroup(ui: UI, withSkill?: SkillID) {
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

export async function renameGroup(ui: UI, s: Snapshot, groupID: GroupID) {
  const current = groupName(s, groupID);
  const t = messages().actions;
  const name = await ui.prompt({ title: t.renameGroup, initial: current, confirm: t.rename });
  if (!name || name === current) return;
  await ui.run(() => LibraryService.RenameGroup(groupID, name), {
    success: (g) => (g.name !== name ? t.groupRenamedAs(g.name) : t.groupRenamed(g.name)),
  });
}

export async function deleteGroup(ui: UI, s: Snapshot, groupID: GroupID) {
  // 有组安装时，只随本组安装的技能由用户决定去留，与移出成员一致。
  const installs = s.groupInstalls.filter((a) => a.group_id === groupID).length;
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.deleteGroupTitle(groupName(s, groupID)),
    body: (
      <>
        <Hint>{t.deleteGroupHint(installs)}</Hint>
        <DetachNotice
          s={s}
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

// ---------- 重新安装 ----------

const placeKey = (p: { skill_id: SkillID; scope: Scope }) => `${p.skill_id}|${scopeKey(p.scope)}`;

// reinstall 把 filter 范围内不是当前版本的各处换成当前版本。先现查状态：快照可能已落后于磁盘，
// 而重新安装只换列出的几处。被改过的几处逐处让用户勾选是否覆盖，没勾选的保持原样。
// 文件夹不在了的几处可能是用户有意删掉的：只点了这一处时直接装回去;批量时逐处让用户选不动、装回去还是卸载，默认不动。
// what 是重新安装的是什么：技能名(经 reinstallSkill 加上引号)或作用域。
export async function reinstall(ui: UI, s: Snapshot, filter: SkillScopeFilter, what: string) {
  const t = messages().actions;
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes(filter), { reload: false });
  if (inspected === undefined) return;
  const targets = (inspected ?? []).filter(needsReinstall);
  if (!targets.length) {
    ui.toast(t.allCurrent(what));
    return;
  }
  const single = !!filter.skill_id && !!filter.scope;
  const modified = targets.filter((p) => p.status === SkillScopeStatus.SkillScopeModified);
  const missing = single ? [] : targets.filter((p) => p.status === SkillScopeStatus.SkillScopeMissing);
  let chosen: ReinstallChoices = { overwrite: new Set(), missing: new Map() };
  if (modified.length || missing.length) {
    const picked = await ui.open<ReinstallChoices>((close) => <ReinstallDialog s={s} modified={modified} missing={missing} close={close} />);
    if (!picked) return;
    chosen = picked;
  }
  const choice = (p: SkillScopeInspection) => chosen.missing.get(folderKey(p)) ?? "keep";
  const wanted = (p: SkillScopeInspection) => {
    if (p.status === SkillScopeStatus.SkillScopeModified) return chosen.overwrite.has(placeKey(p));
    if (p.status === SkillScopeStatus.SkillScopeMissing) return single || choice(p) === "putBack";
    return true;
  };
  const requests: ReinstallRequest[] = targets.filter(wanted).map((p) => ({
    skill_id: p.skill_id,
    scope: p.scope,
    overwrite: p.status === SkillScopeStatus.SkillScopeModified || p.status === SkillScopeStatus.SkillScopeMissing,
  }));
  // 共用一个文件夹的几处卸载一次就都撤销了，只卸载其中一处。
  const uninstalls = [...new Map(missing.filter((p) => choice(p) === "uninstall").map((p) => [folderKey(p), p])).values()];
  const keptModified = modified.length - modified.filter(wanted).length;
  const keptMissing = new Set(missing.filter((p) => choice(p) === "keep").map(folderKey)).size;
  if (!requests.length && !uninstalls.length) {
    ui.toast(t.nothingReinstalled(what, keptModified, keptMissing));
    return;
  }
  const requested = new Set(requests.map(placeKey));
  const written = (p: SkillScopeInspection) => requested.has(placeKey(p));
  const explained = (p: SkillScopeInspection) => p.status === SkillScopeStatus.SkillScopeModified;
  if (requests.length && !(await confirmLinkWrites(ui, s, inspected ?? [], written, t.reinstallTitle(what), t.reinstall, explained))) return;
  const done = await ui.run(async () => {
    const results = requests.length ? ((await LibraryService.Reinstall(requests)) ?? []) : [];
    const rows: ResultRow[] = [];
    let uninstalled = 0;
    for (const p of uninstalls) {
      try {
        await LibraryService.Uninstall(p.skill_id, p.scope);
        uninstalled++;
      } catch (e) {
        const err = toLibraryError(e);
        if (err.code === ErrorCode.Canceled) throw e;
        rows.push({ label: `${skillName(s, p.skill_id)} · ${scopeLabel(s, p.scope)}`, outcome: t.notUninstalled, tone: "bad", reason: describeError(err) });
      }
    }
    return { results, rows, uninstalled };
  });
  if (!done) return;
  const failed = done.results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed);
  if (failed.length || done.rows.length) {
    await showResults(ui, t.reinstallPartly, [...failed.map((r) => failedRow(s, r)), ...done.rows], t.kept(keptModified, keptMissing) || undefined);
    return;
  }
  const placed = done.results.filter((r) => r.outcome === ReinstallOutcome.Reinstalled).length;
  ui.toast(t.reinstalled(placed, done.uninstalled, what, keptModified, keptMissing));
}

type MissingChoice = "keep" | "putBack" | "uninstall";

// ReinstallChoices 是确认框里的选择:被改过的勾选了哪几处覆盖(placeKey),文件夹不在了的每个文件夹选了什么(folderKey)。
interface ReinstallChoices {
  overwrite: Set<string>;
  missing: Map<string, MissingChoice>;
}

// ReinstallDialog 在重新安装前问用户两类地方：文件夹不在了的(不动、装回去还是卸载，默认不动)与被改过的(勾选的才覆盖，默认都不覆盖)。
function ReinstallDialog({
  s,
  modified,
  missing,
  close,
}: {
  s: Snapshot;
  modified: SkillScopeInspection[];
  missing: SkillScopeInspection[];
  close: (value?: ReinstallChoices) => void;
}) {
  const t = useT();
  const [overwrite, setOverwrite] = useState<Set<string>>(new Set());
  const [choices, setChoices] = useState<Map<string, MissingChoice>>(new Map());
  const folders = [...new Map(missing.map((p) => [folderKey(p), missing.filter((x) => folderKey(x) === folderKey(p))])).entries()];
  const choose = (key: string, value: MissingChoice) => setChoices((prev) => new Map(prev).set(key, value));
  const toggle = (key: string) =>
    setOverwrite((prev) => {
      const next = new Set(prev);
      if (!next.delete(key)) next.add(key);
      return next;
    });
  return (
    <Modal
      title={missing.length ? t.actions.reinstallConfirmTitle : t.actions.modifiedTitle}
      onClose={() => close()}
      footer={
        <>
          {folders.length > 0 && (
            <Button className="mr-auto" onClick={() => setChoices(new Map(folders.map(([key]) => [key, "putBack"])))}>
              {t.actions.putBackAll}
            </Button>
          )}
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant={overwrite.size ? "danger" : "primary"} onClick={() => close({ overwrite, missing: choices })} autoFocus>
            {missing.length ? t.common.ok : overwrite.size ? t.actions.reinstallOverwrite(overwrite.size) : t.actions.reinstall}
          </Button>
        </>
      }
    >
      {folders.length > 0 && (
        <>
          <Notice tone="warn">{t.actions.missingNotice(folders.length)}</Notice>
          <ListBox>
            {folders.map(([key, places]) => {
              const p = places[0];
              const groups = [...new Set(places.flatMap((x) => x.groups ?? []))];
              const shared = p.shared_with ?? [];
              return (
                <ListRow key={key} compact>
                  <div className="min-w-0 grow">
                    <div>
                      {t.actions.missingPlace(skillName(s, p.skill_id), places.map((x) => scopeLabel(s, x.scope)))}
                    </div>
                    <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
                    {shared.length > 0 && (
                      <div className="text-[11px] text-muted-foreground">{t.actions.missingShared(shared.map((code) => agentName(s, code)))}</div>
                    )}
                    {groups.length > 0 && (
                      <div className="text-[11px] text-muted-foreground">{t.actions.missingGrouped(groups.map((g) => groupName(s, g)))}</div>
                    )}
                  </div>
                  <Segmented
                    className="shrink-0"
                    value={choices.get(key) ?? "keep"}
                    onChange={(value) => choose(key, value)}
                    options={[
                      { value: "keep", label: t.actions.leaveAlone },
                      { value: "putBack", label: t.actions.putBack },
                      ...(groups.length ? [] : [{ value: "uninstall" as const, label: t.actions.uninstall }]),
                    ]}
                  />
                </ListRow>
              );
            })}
          </ListBox>
        </>
      )}
      {modified.length > 0 && (
        <>
          <Notice tone="warn">{t.actions.modifiedNotice}</Notice>
          <ListBox>
            {modified.map((p) => {
              const key = placeKey(p);
              return (
                <ListRow key={key} asChild compact clickable selected={overwrite.has(key)}>
                  <label>
                    <input type="checkbox" className="shrink-0 accent-primary" checked={overwrite.has(key)} onChange={() => toggle(key)} />
                    <div className="min-w-0 grow">
                      <div>
                        {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
                      </div>
                      <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
                      {p.link_target && <div className="text-[11px] break-all text-muted-foreground">{linkNote(s, p.link_target)}</div>}
                    </div>
                  </label>
                </ListRow>
              );
            })}
          </ListBox>
        </>
      )}
    </Modal>
  );
}

// ---------- 符号链接 ----------
// 库写到是符号链接的那一处时写成独立的一份;覆盖或删除一处时，别处没一起动的链接若指着它，先给那一处换上独立的一份、
// 保持它现在读到的内容。写之前、删之前都在确认里说出来。

type Detached = { place: SkillScopeInspection; by: Scope[] };

// folderPlaces 是和 places 同一个文件夹的各处(含共用这个目录的 Agent),删文件夹时它们一起没了。
export function folderPlaces(s: Snapshot, places: SkillScopeInspection[]): SkillScopeInspection[] {
  const folders = new Set(places.map((p) => `${p.skill_id}|${p.folder}`));
  return s.skillScopes.filter((p) => folders.has(`${p.skill_id}|${p.folder}`));
}

// groupOnly 是这个组在它装到的各作用域里，只靠它留着的成员(没有直接安装、也没有别的组安装):选了一并卸载时这些文件夹会删。
function groupOnly(s: Snapshot, groupID: GroupID, members: SkillID[]): SkillScopeInspection[] {
  const scopes = new Set(s.groupInstalls.filter((g) => g.group_id === groupID).map((g) => scopeKey(g.scope)));
  return s.skillScopes.filter(
    (p) => members.includes(p.skill_id) && scopes.has(scopeKey(p.scope)) && !p.direct && (p.groups ?? []).every((g) => g === groupID),
  );
}

// detachedBy 是 touched 这几处(要覆盖或删除的)被别处的链接指着、而那一处不在 touched 里的:库会先给那一处换上独立的一份。
// 共用一个文件夹的几处只算一次。
export function detachedBy(touched: SkillScopeInspection[]): Detached[] {
  const keys = new Set(touched.map(placeKey));
  const folders = new Set<string>();
  return touched.flatMap((place) => {
    const folder = `${place.skill_id}|${place.folder}`;
    if (folders.has(folder)) return [];
    folders.add(folder);
    const by = (place.linked_by ?? []).filter((scope) => !keys.has(placeKey({ skill_id: place.skill_id, scope })));
    return by.length ? [{ place, by }] : [];
  });
}

// DetachNotice 说明 detachedBy 找出的那几处会先换上独立的一份;when 是在删、换还是搬走这个文件夹之前,
// prefix 用在只和某个选项有关时(如"选「一并卸载」时:")。
export function DetachNotice({
  s,
  items,
  when,
  prefix = "",
}: {
  s: Snapshot;
  items: Detached[];
  when: "delete" | "replace" | "move";
  prefix?: string;
}) {
  const t = useT();
  if (!items.length) return null;
  return (
    <Notice className="detach-linked">
      {prefix}
      {items.map(({ place, by }) => (
        <span key={placeKey(place)} className="block">
          {t.actions.detach(
            by.map((scope) => scopeLabel(s, scope)),
            skillName(s, place.skill_id),
            shortPath(s, place.folder),
            when,
          )}
        </span>
      ))}
    </Notice>
  );
}

// linkWrites 是写这几处(written)时和符号链接有关的两种情况:links 是写的就是链接(会换成独立的一份),
// kept 是别处没一起写的链接指着要写的地方(先换成独立的一份，保持原来的内容)。
export function linkWrites(places: SkillScopeInspection[], written: (p: SkillScopeInspection) => boolean) {
  const chosen = places.filter(written);
  return { links: chosen.filter((p) => p.link_target), kept: detachedBy(chosen) };
}

export function LinkWriteNotes({ s, links, kept }: { s: Snapshot } & ReturnType<typeof linkWrites>) {
  const t = useT();
  return (
    <>
      {links.length > 0 && (
        <>
          <Notice className="write-linked">{t.actions.writeLinked(links.length)}</Notice>
          <LinkedPlaces s={s} places={links} />
        </>
      )}
      <DetachNotice s={s} items={kept} when="replace" />
    </>
  );
}

// confirmLinkWrites 在写这几处之前说明 linkWrites 找出的情况;都没有时不打扰用户。explained 是前一个弹窗里已经逐行说过的链接。
export async function confirmLinkWrites(
  ui: UI,
  s: Snapshot,
  places: SkillScopeInspection[],
  written: (p: SkillScopeInspection) => boolean,
  title: string,
  confirm: string,
  explained: (p: SkillScopeInspection) => boolean = () => false,
): Promise<boolean> {
  const { links, kept } = linkWrites(places, written);
  const unexplained = links.filter((p) => !explained(p));
  if (!unexplained.length && !kept.length) return true;
  return !!(await ui.confirm({ title, body: <LinkWriteNotes s={s} links={unexplained} kept={kept} />, confirm }));
}

// confirmLinkedRequests 是 confirmLinkWrites 的现查版：只知道要写哪几处时，先巡检一次再说明。
export async function confirmLinkedRequests(ui: UI, s: Snapshot, requests: { skill_id: SkillID; scope: Scope }[], title: string, confirm: string) {
  const wanted = new Set(requests.map(placeKey));
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes({}), { reload: false });
  if (inspected === undefined) return false;
  return confirmLinkWrites(ui, s, inspected ?? [], (p) => wanted.has(placeKey(p)), title, confirm);
}

// LinkedPlaces 列出是符号链接的各处与它们指向哪里。
export function LinkedPlaces({ s, places }: { s: Snapshot; places: SkillScopeInspection[] }) {
  return (
    <ListBox>
      {places.map((p) => (
        <ListRow key={placeKey(p)} compact className="linked-place">
          <div className="min-w-0 grow">
            <div>
              {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
            </div>
            <div className="truncate font-mono text-[11px] text-muted-foreground" title={p.link_target}>
              {shortPath(s, p.folder)} → {shortPath(s, p.link_target!)}
            </div>
          </div>
        </ListRow>
      ))}
    </ListBox>
  );
}

// failedRow 只用于重新安装失败的条目;成功的只汇总计数。
function failedRow(s: Snapshot, r: ReinstallResult): ResultRow {
  return {
    label: `${skillName(s, r.skill_id)} · ${scopeLabel(s, r.scope)}`,
    outcome: messages().actions.failed,
    tone: "bad",
    reason: r.error ? describeError(r.error) : undefined,
  };
}

// ---------- 更新 ----------

// checkUpdates 按集合检查更新(只读);没读成来源的集合列出原因。汇报的数目只算计入角标的(跳过的、选了继续保留的不算)。
export async function checkUpdates(ui: UI, s: Snapshot, collections: CollectionID[]) {
  const t = messages().actions;
  const checks = await ui.run(() => LibraryService.CheckUpdates(collections));
  if (!checks) return;
  const skills = checks.flatMap((c) => c.skills ?? []).filter(pending);
  const available = skills.filter(updateAvailable).length;
  const renamed = skills.filter(renamedTo).length;
  const deleted = skills.filter(deletedInSource).length;
  const summary = t.checkSummary(checks.length, available, renamed, deleted);
  const failed = checks.filter((c) => c.error);
  if (failed.length) {
    await showResults(
      ui,
      t.checkUpdates,
      failed.map((c) => ({
        label: c.source?.label ? sourceLabel(s, c.source) : t.collectionFallback(c.collection_id),
        outcome: t.checkFailed,
        tone: "bad" as const,
        reason: describeError(c.error!),
      })),
      t.checkFailedNote(summary, failed.length),
    );
  } else {
    ui.toast(skills.length ? summary : t.nothingPending(checks.length));
  }
}

// previewCollection 对库里已有的集合再做一次入库预览，看作者又发布了哪些技能;已入库的候选标出来、不能再选。
export async function previewCollection(ui: UI, collectionID: CollectionID) {
  const preview = await ui.run(() => LibraryService.PreviewFetch({ collection: collectionID }), { reload: false });
  if (!preview) return;
  await ui.open((close) => <FetchReview preview={preview} onClose={() => close()} onBack={() => close()} />);
}

// ---------- 移除技能 ----------

// lostEdits 是删除这些文件夹时有几处的改动会丢掉(状态取自快照里的巡检;共用目录的几个 Agent 共用一个文件夹，只算一处)。
const lostEdits = (places: SkillScopeInspection[]) =>
  new Set(places.filter((p) => p.status === SkillScopeStatus.SkillScopeModified).map((p) => p.folder)).size;

export async function removeSkill(ui: UI, s: Snapshot, id: SkillID) {
  const places = skillScopesOf(s, id);
  const installed = places.length;
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

// ---------- 项目 ----------

// registerProject 登记项目;登记之后把用户带到这个项目里不是库装的技能，一次确认全部入库。
export async function registerProject(ui: UI, path: string): Promise<boolean> {
  const project = await ui.run(() => LibraryService.RegisterProject(path), {
    success: (p) => messages().actions.projectRegistered(p.path),
  });
  if (project) void offerProject(ui, project);
  return !!project;
}

export async function relocateProject(ui: UI, project: RegisteredProject, path: string): Promise<boolean> {
  const moved = await ui.run(() => LibraryService.RelocateProject(project.id, path), {
    success: (p) => messages().actions.projectRelocated(p.name, p.path),
  });
  return !!moved;
}

export async function unregisterProject(ui: UI, s: Snapshot, project: RegisteredProject) {
  const places = s.skillScopes.filter((p) => p.scope.project_id === project.id);
  const installed = places.length;
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.unregisterTitle(projectName(s, project.id)),
    body: (
      <>
        <Hint>{t.unregisterHint(shortPath(s, project.path))}</Hint>
        <DetachNotice s={s} items={detachedBy(places)} when="delete" prefix={t.ifRemoveToo} />
      </>
    ),
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: t.removeProjectFolders,
            desc: t.removeFolders(new Set(places.map((p) => p.folder)).size, lostEdits(places)),
          },
          { value: FolderPolicy.Keep, label: t.keepProjectFolders, desc: t.keepProjectFoldersDesc },
        ]
      : undefined,
    confirm: t.unregister,
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await ui.run(() => LibraryService.UnregisterProject(project.id, { folders: policy }), {
    success: t.unregistered,
  });
}

// ---------- Agent ----------
// 只有自定义 Agent 能改、能删。它离开或删除时，旧目录里还有别的 Agent 在读的文件夹不动，确认框里逐个列出。

// SharedFolders 列出还有别的 Agent 在读、因而保留的技能文件夹。
function SharedFolders({ s, places }: { s: Snapshot; places: SkillScopeInspection[] }) {
  const t = useT();
  return (
    <ListBox>
      {places.map((p) => (
        <ListRow key={placeKey(p)}>
          <div className="min-w-0 grow">
            <div>
              {skillName(s, p.skill_id)} · {p.scope.project_id ? projectName(s, p.scope.project_id) : t.model.global}
            </div>
            <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
          </div>
          <span className="text-[11px] text-muted-foreground">{t.actions.sharedBy((p.shared_with ?? []).map((code) => agentName(s, code)))}</span>
        </ListRow>
      ))}
    </ListBox>
  );
}

export async function addAgent(ui: UI, input: CustomAgent, onInvalid: (err: LibraryError) => boolean): Promise<boolean> {
  const added = await ui.run(() => LibraryService.AddAgent(input), {
    success: (a) => messages().actions.agentAdded(a.name),
    onError: onInvalid,
  });
  return !!added;
}

const cleanPath = (path: string) => path.replace(/^\.\//, "").replace(/\/+$/, "");

// editAgent 修改自定义 Agent。某一层级的目录改了、这个 Agent 在那里已装了技能(含因共用目录而算已装的)或有组安装时，
// 先问是否迁移(不迁移会连组安装一起去掉,空组的组安装也是);迁移后列出因新目录已有同名文件夹而没搬的。input.global_directory 是展开 ~ 后的绝对路径。
export async function editAgent(
  ui: UI,
  s: Snapshot,
  agent: Agent,
  input: CustomAgent,
  onInvalid: (err: LibraryError) => boolean,
): Promise<boolean> {
  const t = messages().actions;
  const globalChanged = cleanPath(input.global_directory) !== cleanPath(agent.global_directory);
  const projectChanged = cleanPath(input.project_directory) !== cleanPath(agent.project_directory);
  const changedHere = (scope: Scope) => scope.agent === agent.code && (placeOf(scope) ? projectChanged : globalChanged);
  const affected = s.skillScopes.filter((p) => changedHere(p.scope));
  const groupInstalls = s.groupInstalls.filter((g) => changedHere(g.scope)).length;
  let migrate = false;
  if (affected.length || groupInstalls) {
    const shared = affected.filter((p) => p.shared_with?.length);
    const choice = await ui.confirm({
      title: t.editAgentTitle(agent.name),
      body: (
        <>
          <Hint>{t.editAgentHint(affected.length, groupInstalls)}</Hint>
          {shared.length > 0 && (
            <>
              <Notice>{t.editAgentShared(shared.length)}</Notice>
              <SharedFolders s={s} places={shared} />
            </>
          )}
          <DetachNotice
            s={s}
            items={detachedBy(folderPlaces(s, affected.filter((p) => !p.shared_with?.length)))}
            when="move"
            prefix={t.ifMigrate}
          />
        </>
      ),
      choices: [
        { value: "migrate", label: t.migrate, desc: t.migrateDesc },
        { value: "keep", label: t.noMigrate, desc: t.noMigrateDesc },
      ],
      confirm: t.save,
    });
    if (!choice) return false;
    migrate = choice === "migrate";
  }
  const result = await ui.run(() => LibraryService.EditAgent(agent.code, input, { migrate }), { onError: onInvalid });
  if (!result) return false;
  const name = result.agent.name;
  const migrations = result.migrations ?? [];
  const skipped = migrations.filter((m) => m.outcome === MigrationOutcome.MigrationSkipped);
  if (skipped.length) {
    await showResults(
      ui,
      t.agentSaved(name),
      skipped.map((m) => ({
        label: `${skillName(s, m.skill_id)} · ${m.scope.project_id ? projectName(s, m.scope.project_id) : messages().model.global}`,
        outcome: t.notMigrated,
        tone: "warn" as const,
        reason: t.notMigratedReason(shortPath(s, m.to), shortPath(s, m.from)),
      })),
      t.migratedPartly(migrations.length - skipped.length, skipped.length),
    );
  } else {
    ui.toast(migrations.length ? t.agentSavedMigrated(name, migrations.length) : t.agentSaved(name));
  }
  return true;
}

// deleteAgent 删除自定义 Agent,和注销项目一样让用户选文件夹的去留;还有别的 Agent 在读的文件夹不管怎么选都保留。
export async function deleteAgent(ui: UI, s: Snapshot, agent: Agent) {
  const places = s.skillScopes.filter((p) => p.scope.agent === agent.code);
  const kept = places.filter((p) => p.shared_with?.length);
  const own = places.filter((p) => !p.shared_with?.length);
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.deleteAgentTitle(agent.name),
    body: (
      <>
        <Hint>{t.deleteAgentHint}</Hint>
        {kept.length > 0 && (
          <>
            <Notice>{t.deleteAgentShared(kept.length)}</Notice>
            <SharedFolders s={s} places={kept} />
          </>
        )}
        <DetachNotice s={s} items={detachedBy(folderPlaces(s, own))} when="delete" prefix={t.ifRemoveToo} />
      </>
    ),
    choices: own.length
      ? [
          {
            value: FolderPolicy.Remove,
            label: t.removeInstalled,
            desc: t.removeFolders(own.length, lostEdits(own)),
          },
          { value: FolderPolicy.Keep, label: t.keepInstalled, desc: t.keepAgentFoldersDesc },
        ]
      : undefined,
    confirm: t.delete,
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await ui.run(() => LibraryService.DeleteAgent(agent.code, { folders: policy }), {
    success: t.agentDeleted(agent.name),
  });
}

// fetchUnmanaged 把作用域里一个不是库装的技能(未纳管技能)入库:磁盘上什么都不改，文件夹从此由 app 管。
// 确认时写清代价：之后还用命令行工具更新它，app 只能报为被手动改过;提交在项目仓库里的，之后在 app 里更新会改动仓库里的文件。
// 来源链接预填 npx skills 来源记录里认出的来源，认不出时预填在 skills.sh 上找到的来源，用户可以改或清空;清空就是没有来源的技能。
// 内容和库里某个技能的某个版本一致时，技能库只给那个技能补记这一处安装。
export async function fetchUnmanaged(ui: UI, s: Snapshot, skill: UnmanagedSkill) {
  const t = messages().actions;
  const place = scopeLabel(s, skill.scope);
  // 库里已有同名的技能时确认里说出来：内容不同不合并，入库后会有两个同名的。
  const twins = s.skills
    .filter((x) => x.name === skill.name)
    .map((x) => t.twin(noSource(x.source) ? messages().model.noSource : sourceLabel(s, x.source), installedWhere(s, x.id)));
  const outside = skill.link_outside && skill.link_target ? outsideLinkNote(shortPath(s, skill.link_target)) : undefined;
  const r = await ui.open<FetchUnmanagedResult>((close) => <FetchUnmanagedDialog place={place} skill={skill} twins={twins} outside={outside} close={close} />);
  if (!r) return;
  if (r.matched) ui.toast(t.matchedVersion(skill.name, r.skill.name, place));
  else if (r.skill.collection_id) ui.toast(t.fetchedTracking(r.skill.name, r.skill.source.label));
  else ui.toast(t.fetched(r.skill.name));
}

// FetchUnmanagedDialog 是入库确认：来源链接可改可清空;读来源失败时留在框里说明原因，用户可以重试、改链接或清空后入库。
function FetchUnmanagedDialog({
  place,
  skill,
  twins,
  outside,
  close,
}: {
  place: string;
  skill: UnmanagedSkill;
  twins: string[];
  outside?: string; // 指向 Agent 目录以外的符号链接时的说明
  close: (value?: FetchUnmanagedResult) => void;
}) {
  const ui = useUI();
  const t = useT();
  // 确认框开着时才在 skills.sh 上找到来源的，用户还没动过链接就补上。
  const found = foundSource(useFoundSources(), skill);
  const [edited, setLink] = useState<string>();
  const link = edited ?? skill.source_input ?? found ?? "";
  const [error, setError] = useState<LibraryError>();
  const [busy, setBusy] = useState(false);
  const input = link.trim();
  // 来源记录里的分支只跟着它认出的那个来源走;用户改了链接就不再带上。
  const ref = input && input === skill.source_input ? skill.source_ref : undefined;
  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError(undefined);
    const r = await ui.run(
      () => LibraryService.FetchUnmanaged({ scope: skill.scope, name: skill.name, source_input: input || undefined, source_ref: ref }),
      { onError: (err) => (setError(err), true) },
    );
    setBusy(false);
    if (r) close(r);
  };
  return (
    <Modal
      title={t.actions.fetchTitle(skill.name)}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="primary" onClick={() => void submit()} disabled={busy} autoFocus>
            {t.actions.fetch}
          </Button>
        </>
      }
    >
      <Hint>{t.actions.fetchHandOver(place)}</Hint>
      <Hint>{t.actions.fetchCliCost}</Hint>
      {skill.in_repository && <Notice tone="warn">{t.actions.fetchInRepository}</Notice>}
      {outside && (
        <Notice tone="warn" className="unmanaged-link-outside">
          {outside}
        </Notice>
      )}
      {twins.length > 0 && (
        <Notice tone="warn" className="unmanaged-same-name">
          {t.actions.sameNameTwins(skill.name, twins, sameNameFetchNote(skill.name, !!input))}
        </Notice>
      )}
      <Field label={t.actions.sourceLink}>
        <Input
          className="unmanaged-source font-mono"
          value={link}
          placeholder={t.actions.sourceLinkPlaceholder}
          onChange={(e) => setLink(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && void submit()}
        />
      </Field>
      <Hint className="unmanaged-source-note">
        {input ? t.actions.sourceNote(input === found, `${input}${ref ? ` · ${ref}` : ""}`) : t.actions.noSourceNote}
      </Hint>
      {error && (
        <Notice tone="bad" className="unmanaged-error whitespace-pre-line">
          {unmanagedSourceError(error, skill.name)}
        </Notice>
      )}
    </Modal>
  );
}

