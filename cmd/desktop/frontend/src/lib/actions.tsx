// 写操作的公共流程：卡片开关、详情抽屉、整组开关与拖放都走这里，冲突处理只写一份。
// 规则来自技能库的契约(internal/library/api.go):
// - 安装前先看安装计划：未记录的外来文件夹可选覆盖;被另一个技能占用或作用域根目录不存在时只能说明原因。
// - 组安装 / 加入组 / 重新安装逐条返回结果，被跳过或失败的条目列出原因。
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ConflictPolicy,
  ErrorCode,
  FolderPolicy,
  GroupMemberOutcome,
  InstallPlanStatus,
  MemberRemovalPolicy,
  ReinstallOutcome,
  type GroupID,
  type GroupMemberResult,
  type RegisteredProject,
  type ReinstallResult,
  type Scope,
  type SkillID,
  type SkillScopeFilter,
  type SkillSummary,
} from "@bindings/internal/library";
import { describeError } from "./errors";
import type { Snapshot } from "./library";
import { projectName, scopeLabel, shortPath, skillScopesOf } from "./model";
import { Modal, type useUI } from "./ui";

type UI = ReturnType<typeof useUI>;

const skillName = (s: Snapshot, id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;
const groupName = (s: Snapshot, id: GroupID) => s.groups.find((g) => g.group.id === id)?.group.name ?? `#${id}`;

// ---------- 结果列表 ----------

interface ResultRow {
  label: string;
  outcome: string;
  tone: "ok" | "warn" | "bad";
  reason?: string;
}

function showResults(ui: UI, title: string, rows: ResultRow[], note?: string) {
  return ui.open((close) => (
    <Modal
      title={title}
      onClose={() => close()}
      footer={
        <button type="button" className="pill-btn accent" onClick={() => close()}>
          知道了
        </button>
      }
    >
      {note && <div className="notice">{note}</div>}
      <div className="list-box">
        {rows.map((r, i) => (
          <div key={i} className="list-row">
            <div className="grow">
              <div>{r.label}</div>
              {r.reason && <div className="desc" style={{ whiteSpace: "pre-line" }}>{r.reason}</div>}
            </div>
            <span className={`status ${r.tone}`}>{r.outcome}</span>
          </div>
        ))}
      </div>
    </Modal>
  ));
}

const memberOutcome: Record<string, { text: string; tone: ResultRow["tone"] }> = {
  [GroupMemberOutcome.MemberInstalled]: { text: "已安装", tone: "ok" },
  [GroupMemberOutcome.MemberAlreadyInstalled]: { text: "原已安装", tone: "ok" },
  [GroupMemberOutcome.MemberSkipped]: { text: "已跳过", tone: "warn" },
};

function memberRows(s: Snapshot, results: GroupMemberResult[]): ResultRow[] {
  return results.map((r) => ({
    label: `${skillName(s, r.skill_id)} → ${scopeLabel(s, r.scope)}`,
    outcome: memberOutcome[r.outcome]?.text ?? r.outcome,
    tone: memberOutcome[r.outcome]?.tone ?? "warn",
    reason: r.error ? describeError(r.error) : undefined,
  }));
}

function summarizeMembers(results: GroupMemberResult[]): string {
  const count = (o: GroupMemberOutcome) => results.filter((r) => r.outcome === o).length;
  const parts = [
    `新安装 ${count(GroupMemberOutcome.MemberInstalled)}`,
    `原已安装 ${count(GroupMemberOutcome.MemberAlreadyInstalled)}`,
  ];
  const skipped = count(GroupMemberOutcome.MemberSkipped);
  if (skipped) parts.push(`跳过 ${skipped}`);
  return parts.join(" · ");
}

// ---------- 单个技能的安装 ----------

export async function installSkill(ui: UI, s: Snapshot, id: SkillID, scope: Scope) {
  const plan = await ui.run(() => LibraryService.PlanInstall(id, scope), { reload: false });
  if (!plan) return;
  const where = scopeLabel(s, scope);
  let policy = ConflictPolicy.Fail;
  switch (plan.status) {
    case InstallPlanStatus.InstallOccupied:
      ui.toast(
        `无法安装：目标文件夹已被「${plan.occupier_name || skillName(s, plan.occupier_skill_id ?? 0)}」占用\n${shortPath(s, plan.target)}\n先卸载占用者，再安装`,
        "error",
      );
      return;
    case InstallPlanStatus.InstallUntracked: {
      const ok = await ui.confirm({
        title: "目标已有同名文件夹",
        body: (
          <div className="notice warn">
            {shortPath(s, plan.target)} 已存在，但不是技能库写入的。覆盖会用「{plan.name}」替换它。
          </div>
        ),
        confirm: "覆盖并安装",
        danger: true,
      });
      if (!ok) return;
      policy = ConflictPolicy.Overwrite;
      break;
    }
  }
  await ui.run(() => LibraryService.Install(id, scope, { on_conflict: policy }), {
    success: `已安装「${plan.name}」到 ${where}`,
  });
}

export async function uninstallSkill(ui: UI, s: Snapshot, id: SkillID, scope: Scope) {
  await ui.run(() => LibraryService.Uninstall(id, scope), {
    success: `已卸载「${skillName(s, id)}」在 ${scopeLabel(s, scope)} 的直接安装`,
    onError: (err) => {
      if (err.code !== ErrorCode.FailedPrecondition) return false;
      ui.toast("这个技能只随组安装，没有直接安装可卸载;请在对应的组上卸载", "error");
      return true;
    },
  });
}

// ---------- 组安装 ----------

export async function installGroup(ui: UI, s: Snapshot, groupID: GroupID, scope: Scope) {
  const plan = await ui.run(() => LibraryService.PlanGroupInstall(groupID, scope), { reload: false });
  if (!plan) return;
  const members = plan.members ?? [];
  const untracked = members.filter((m) => m.status === InstallPlanStatus.InstallUntracked);
  const blocked = members.filter(
    (m) => m.status === InstallPlanStatus.InstallOccupied || m.status === InstallPlanStatus.InstallDuplicate,
  );
  let policy = ConflictPolicy.Fail;
  if (untracked.length) {
    const choice = await ui.confirm({
      title: `把「${groupName(s, groupID)}」组安装到 ${scopeLabel(s, scope)}`,
      body: (
        <div className="notice warn">
          {untracked.length} 个成员的目标位置已有不是技能库写入的同名文件夹:
          {untracked.map((m) => m.name).join("、")}
        </div>
      ),
      choices: [
        { value: "skip", label: "跳过这些成员", desc: "其余成员照常安装，之后可再次组安装补齐" },
        { value: "overwrite", label: "覆盖这些文件夹", desc: "用技能库里的版本替换它们" },
      ],
      confirm: "安装",
    });
    if (!choice) return;
    if (choice === "overwrite") policy = ConflictPolicy.Overwrite;
  }
  const results = await ui.run(() => LibraryService.InstallGroup(groupID, scope, { on_conflict: policy }));
  if (!results) return;
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  if (skipped.length || blocked.length) {
    await showResults(ui, `组安装「${groupName(s, groupID)}」`, memberRows(s, results), summarizeMembers(results));
  } else {
    ui.toast(`已把「${groupName(s, groupID)}」组安装到 ${scopeLabel(s, scope)}\n${summarizeMembers(results)}`);
  }
}

export async function uninstallGroup(ui: UI, s: Snapshot, groupID: GroupID, scope: Scope) {
  await ui.run(() => LibraryService.UninstallGroup(groupID, scope), {
    success: `已卸载「${groupName(s, groupID)}」在 ${scopeLabel(s, scope)} 的组安装`,
  });
}

// ---------- 组成员与组本身 ----------

export async function addSkillToGroup(ui: UI, s: Snapshot, groupID: GroupID, id: SkillID) {
  const results = await ui.run(() => LibraryService.AddSkillToGroup(groupID, id));
  if (!results) return;
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  if (skipped.length) {
    await showResults(
      ui,
      `「${skillName(s, id)}」已加入「${groupName(s, groupID)}」`,
      memberRows(s, results),
      "成员关系已成立;下面这些作用域没能安装，可在处理冲突后再次组安装补齐。",
    );
  } else {
    const where = results.length ? `,并安装到该组已组安装的 ${results.length} 个作用域` : "";
    ui.toast(`已把「${skillName(s, id)}」加入「${groupName(s, groupID)}」${where}`);
  }
}

export async function removeSkillFromGroup(ui: UI, s: Snapshot, groupID: GroupID, id: SkillID) {
  // 该组安装到的作用域上，这个技能可能只随本组安装;有组安装时让用户决定去留。
  const installed = s.groupInstalls.filter((a) => a.group_id === groupID);
  let policy = MemberRemovalPolicy.KeepAsDirectInstall;
  if (installed.length) {
    const choice = await ui.confirm({
      title: `把「${skillName(s, id)}」移出「${groupName(s, groupID)}」`,
      body: <p className="hint">该组已组安装到 {installed.length} 个作用域。只随本组安装的技能如何处理?</p>,
      choices: [
        { value: MemberRemovalPolicy.KeepAsDirectInstall, label: "保留", desc: "改为直接安装，磁盘不动" },
        { value: MemberRemovalPolicy.UninstallMember, label: "一并卸载", desc: "从这些作用域的目录里移除文件夹" },
      ],
      confirm: "移出",
    });
    if (!choice) return;
    policy = choice as MemberRemovalPolicy;
  }
  await ui.run(() => LibraryService.RemoveSkillFromGroup(groupID, id, { if_no_other_install: policy }), {
    success: `已把「${skillName(s, id)}」移出「${groupName(s, groupID)}」`,
  });
}

export async function createGroup(ui: UI, withSkill?: SkillID) {
  const name = await ui.prompt({ title: "新建组", label: "按场景命名，例如「前端调试」", confirm: "创建" });
  if (!name) return;
  const group = await ui.run(() => LibraryService.CreateGroup(name), {
    success: (g) => (g.name !== name ? `名字已被占用，已创建为「${g.name}」` : `已创建组「${g.name}」`),
    reload: !withSkill,
  });
  if (group && withSkill) await ui.run(() => LibraryService.AddSkillToGroup(group.id, withSkill));
  return group;
}

export async function renameGroup(ui: UI, s: Snapshot, groupID: GroupID) {
  const current = groupName(s, groupID);
  const name = await ui.prompt({ title: "重命名组", initial: current, confirm: "重命名" });
  if (!name || name === current) return;
  await ui.run(() => LibraryService.RenameGroup(groupID, name), {
    success: (g) => (g.name !== name ? `名字已被占用，已改为「${g.name}」` : `已重命名为「${g.name}」`),
  });
}

export async function deleteGroup(ui: UI, s: Snapshot, groupID: GroupID) {
  const installs = s.groupInstalls.filter((a) => a.group_id === groupID).length;
  const ok = await ui.confirm({
    title: `删除组「${groupName(s, groupID)}」`,
    body: (
      <p className="hint">
        成员技能不会被删除。{installs ? `该组的 ${installs} 个组安装会一并删除;` : ""}
        只随本组安装的技能会转为直接安装，磁盘上的文件夹不动。
      </p>
    ),
    confirm: "删除组",
    danger: true,
  });
  if (!ok) return;
  await ui.run(() => LibraryService.DeleteGroup(groupID), { success: "已删除组" });
}

// ---------- 重新安装 ----------

export async function reinstall(ui: UI, s: Snapshot, filter: SkillScopeFilter, what: string) {
  const results = await ui.run(() => LibraryService.Reinstall(filter, { on_conflict: ConflictPolicy.Fail }));
  if (!results) return;
  const failed = results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed);
  const modified = failed.filter((r) => r.error?.code === ErrorCode.FailedPrecondition && r.error?.details?.path);
  const rows = (list: ReinstallResult[]) => list.map((r) => failedRow(s, r));
  if (!failed.length) {
    const done = results.filter((r) => r.outcome === ReinstallOutcome.Reinstalled).length;
    ui.toast(done ? `已重新安装 ${done} 处(${what})` : `${what}已安装的都是当前版本`);
    return;
  }
  if (modified.length) {
    const ok = await ui.confirm({
      title: "有技能文件夹被手动改过",
      body: (
        <>
          <div className="notice warn">下面这些技能文件夹与任何版本都不一致(可能被手动编辑过)。覆盖会丢掉这些改动。</div>
          <div className="list-box">
            {rows(modified).map((r, i) => (
              <div key={i} className="list-row">
                {r.label}
              </div>
            ))}
          </div>
        </>
      ),
      confirm: "覆盖这些改动",
      danger: true,
    });
    if (ok) {
      const again = await ui.run(() => LibraryService.Reinstall(filter, { on_conflict: ConflictPolicy.Overwrite }));
      const stillFailed = (again ?? []).filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed);
      if (stillFailed.length) await showResults(ui, "部分未能重新安装", rows(stillFailed));
      else if (again) ui.toast(`已重新安装(${what})`);
      return;
    }
  }
  await showResults(ui, "部分未能重新安装", rows(failed));
}

// failedRow 只用于重新安装失败的条目;成功的只汇总计数。
function failedRow(s: Snapshot, r: ReinstallResult): ResultRow {
  return {
    label: `${skillName(s, r.skill_id)} · ${scopeLabel(s, r.scope)}`,
    outcome: "失败",
    tone: "bad",
    reason: r.error ? describeError(r.error) : undefined,
  };
}

// ---------- 更新 ----------

export async function checkUpdates(ui: UI, s: Snapshot, ids: SkillID[]) {
  const checks = await ui.run(() => LibraryService.CheckUpdates(ids));
  if (!checks) return;
  const available = checks.filter((c) => !c.error && c.available).length;
  const failed = checks.filter((c) => c.error);
  if (failed.length) {
    await showResults(
      ui,
      "检查更新",
      failed.map((c) => ({
        label: skillName(s, c.skill_id),
        outcome: "检查失败",
        tone: "bad" as const,
        reason: describeError(c.error!),
      })),
      `检查了 ${checks.length} 个技能:${available} 个有更新，${failed.length} 个失败`,
    );
  } else {
    ui.toast(available ? `${available} 个技能有可用更新` : `检查了 ${checks.length} 个技能，都是最新`);
  }
}

// updateSkill 更新技能库里的版本;已安装的文件夹不会自动跟随，更新后询问是否重新安装。
export async function updateSkill(ui: UI, s: Snapshot, id: SkillID, ask = true) {
  const result = await ui.run(() => LibraryService.Update(id), {
    success: (r) => (r.changed ? `「${r.skill.name}」已更新到新版本` : `「${r.skill.name}」没有变化`),
  });
  if (!result?.changed || !ask) return result;
  const installed = skillScopesOf(s, id).length;
  if (!installed) return result;
  const ok = await ui.confirm({
    title: `把「${result.skill.name}」重新安装到新版本?`,
    body: <p className="hint">它安装在 {installed} 个作用域。更新只改了技能库，那里的文件夹还是旧版本。</p>,
    confirm: "重新安装",
  });
  if (ok) await reinstall(ui, s, { skill_id: id }, `「${result.skill.name}」`);
  return result;
}

export async function updateAll(ui: UI, s: Snapshot, skills: SkillSummary[]) {
  let changed = 0;
  for (const skill of skills) {
    const r = await updateSkill(ui, s, skill.id, false);
    if (r?.changed) changed++;
  }
  ui.toast(`已更新 ${changed} 个技能;已安装的文件夹不会自动跟随，可在作用域摘要里"全部重新安装"`);
}

// ---------- 移除技能 ----------

export async function removeSkill(ui: UI, s: Snapshot, id: SkillID) {
  const installed = skillScopesOf(s, id).length;
  const choice = await ui.confirm({
    title: `移除「${skillName(s, id)}」`,
    body: <p className="hint">从技能库删除它和全部历史版本，也会退出所有组。</p>,
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: "同时删除已安装的文件夹",
            desc: `从 ${installed} 个作用域的目录里移除文件夹`,
          },
          { value: FolderPolicy.Keep, label: "保留已安装的文件夹", desc: "只删除技能库里的记录，文件夹留在原处" },
        ]
      : undefined,
    confirm: "移除",
    danger: true,
  });
  if (!choice) return false;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  const done = await ui.run(() => LibraryService.RemoveSkill(id, { folders: policy }).then(() => true), {
    success: "已移除",
  });
  return !!done;
}

// ---------- 项目 ----------

export async function registerProject(ui: UI, path: string): Promise<boolean> {
  const project = await ui.run(() => LibraryService.RegisterProject(path), {
    success: (p) => `已登记项目 ${p.path}`,
  });
  return !!project;
}

export async function unregisterProject(ui: UI, s: Snapshot, project: RegisteredProject) {
  const installed = s.skillScopes.filter((p) => p.scope.project_id === project.id).length;
  const choice = await ui.confirm({
    title: `注销项目「${projectName(s, project.id)}」`,
    body: (
      <p className="hint">
        {shortPath(s, project.path)} 不再由技能管理器管理，它在各 Agent 下的直接安装与组安装记录都会删除。
      </p>
    ),
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: "同时删除项目里已安装的文件夹",
            desc: `移除 ${installed} 个技能文件夹`,
          },
          { value: FolderPolicy.Keep, label: "保留项目里已安装的文件夹", desc: "文件夹留在项目目录里" },
        ]
      : undefined,
    confirm: "注销",
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await ui.run(() => LibraryService.UnregisterProject(project.id, { folders: policy }), {
    success: "已注销项目",
  });
}
