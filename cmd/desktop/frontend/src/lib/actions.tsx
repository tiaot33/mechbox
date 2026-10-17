// 写操作的公共流程：卡片开关、详情抽屉、整组开关与拖放都走这里，冲突处理只写一份。
// 规则来自技能库的契约(go doc ./internal/library):
// - 安装前先看安装计划：不是从库里装的同名文件夹可选覆盖;被另一个技能占用或作用域根目录不存在时只能说明原因。
// - 重新安装先现查各处状态，只换不是当前版本的几处;被改过的逐处由用户勾选是否覆盖。
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
  type GroupID,
  type GroupMemberResult,
  type RegisteredProject,
  type ReinstallRequest,
  type ReinstallResult,
  type Scope,
  type SkillID,
  type SkillScopeFilter,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { describeError } from "./errors";
import type { LibraryError, Snapshot } from "./library";
import { FetchReview } from "@/components/app/fetch-review";
import { UpdateDialog } from "@/components/app/update-dialog";
import {
  agentName,
  deletedInSource,
  needsReinstall,
  placeOf,
  projectName,
  scopeKey,
  scopeLabel,
  shortPath,
  skillScopeIn,
  skillScopesOf,
  sourceLabel,
  updateAvailable,
} from "./model";
import { Modal, type useUI } from "./ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
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
  return ui.open((close) => (
    <Modal
      title={title}
      onClose={() => close()}
      footer={
        <Button variant="primary" onClick={() => close()}>
          知道了
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
  let overwrite = false;
  switch (plan.status) {
    case InstallPlanStatus.InstallOccupied: {
      // 占用者与本技能同名;用来源区分是哪一个。
      const occupier = s.skills.find((x) => x.id === plan.occupier_skill_id);
      const who =
        occupier?.id === id
          ? "这个技能的另一个作用域"
          : occupier
            ? `来自 ${sourceLabel(s, occupier.source)} 的同名技能`
            : "另一个同名技能";
      ui.toast(`无法安装:${who}已装在\n${shortPath(s, plan.folder)}\n先卸载它，再安装`, "error");
      return;
    }
    case InstallPlanStatus.InstallUntracked: {
      const ok = await ui.confirm({
        title: "目标已有同名文件夹",
        body: (
          <Notice tone="warn">
            {shortPath(s, plan.folder)} 已存在，但不是从技能库装的。覆盖会用「{plan.name}」替换它。
          </Notice>
        ),
        confirm: "覆盖并安装",
        danger: true,
      });
      if (!ok) return;
      overwrite = true;
      break;
    }
  }
  await ui.run(() => LibraryService.Install(id, scope, { overwrite }), {
    success: `已安装「${plan.name}」到 ${where}`,
  });
}

export async function uninstallSkill(ui: UI, s: Snapshot, id: SkillID, scope: Scope) {
  // 共用目录：从其中一个卸载会撤销共用的各个 Agent 的直接安装，文件夹随之删除，先说明还有哪些 Agent 在用。
  const place = skillScopeIn(s, id, scopeKey(scope));
  const shared = place?.shared_with ?? [];
  if (place && shared.length) {
    const ok = await ui.confirm({
      title: `卸载「${skillName(s, id)}」`,
      body: (
        <Notice tone="warn">
          {shortPath(s, place.folder)} 还被 {shared.map((code) => agentName(s, code)).join("、")} 共用。
          {place.groups?.length
            ? "卸载会撤销这几个 Agent 的直接安装;还有组安装留着它，文件夹不删。"
            : "卸载会删除这个文件夹，这几个 Agent 都不再算已装。"}
        </Notice>
      ),
      confirm: "卸载",
      danger: true,
    });
    if (!ok) return;
  }
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
  let overwrite = false;
  if (untracked.length) {
    const choice = await ui.confirm({
      title: `把「${groupName(s, groupID)}」组安装到 ${scopeLabel(s, scope)}`,
      body: (
        <Notice tone="warn">
          {untracked.length} 个成员的目标位置已有不是从技能库装的同名文件夹:
          {untracked.map((m) => m.name).join("、")}
        </Notice>
      ),
      choices: [
        { value: "skip", label: "跳过这些成员", desc: "其余成员照常安装，之后可再次组安装补齐" },
        { value: "overwrite", label: "覆盖这些文件夹", desc: "用技能库里的版本替换它们" },
      ],
      confirm: "安装",
    });
    if (!choice) return;
    overwrite = choice === "overwrite";
  }
  const results = await ui.run(() => LibraryService.InstallGroup(groupID, scope, { overwrite }));
  if (!results) return;
  const skipped = results.filter((r) => r.outcome === GroupMemberOutcome.MemberSkipped);
  if (skipped.length || blocked.length) {
    await showResults(ui, `组安装「${groupName(s, groupID)}」`, memberRows(s, results), summarizeMembers(results));
  } else {
    ui.toast(`已把「${groupName(s, groupID)}」组安装到 ${scopeLabel(s, scope)}\n${summarizeMembers(results)}`);
  }
}

export async function uninstallGroup(ui: UI, s: Snapshot, groupID: GroupID, scope: Scope) {
  // 共用目录：只靠这个组安装留着的成员，卸载后文件夹删除，共用的几个 Agent 都不再算已装，先说明还有哪些 Agent 在用。
  const memberOf = (id: GroupID, skill: SkillID) => !!s.groups.find((g) => g.id === id)?.members?.includes(skill);
  const removed = (s.groups.find((g) => g.id === groupID)?.members ?? [])
    .map((id) => skillScopeIn(s, id, scopeKey(scope)))
    .filter((p): p is SkillScopeInspection => !!p?.shared_with?.length && !p.direct)
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
  if (removed.length) {
    const ok = await ui.confirm({
      title: `卸载「${groupName(s, groupID)}」在 ${scopeLabel(s, scope)} 的组安装`,
      body: (
        <>
          <Notice tone="warn">
            下面 {removed.length} 个技能文件夹还被别的 Agent 共用，只靠这个组安装留着：卸载会删除这些文件夹，共用的几个 Agent 都不再算已装。
          </Notice>
          <SharedFolders s={s} places={removed} />
        </>
      ),
      confirm: "卸载",
      danger: true,
    });
    if (!ok) return;
  }
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
      body: <Hint>该组已组安装到 {installed.length} 个作用域。只随本组安装的技能如何处理?</Hint>,
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
  // 有组安装时，只随本组安装的技能由用户决定去留，与移出成员一致。
  const installs = s.groupInstalls.filter((a) => a.group_id === groupID).length;
  const choice = await ui.confirm({
    title: `删除组「${groupName(s, groupID)}」`,
    body: (
      <Hint>
        成员技能不会被删除。
        {installs ? `该组的 ${installs} 个组安装会一并删除。只随本组安装的技能如何处理?` : ""}
      </Hint>
    ),
    choices: installs
      ? [
          { value: MemberRemovalPolicy.KeepAsDirectInstall, label: "保留", desc: "改为直接安装，磁盘不动" },
          { value: MemberRemovalPolicy.UninstallMember, label: "一并卸载", desc: "从这些作用域的目录里移除文件夹" },
        ]
      : undefined,
    confirm: "删除组",
    danger: true,
  });
  if (!choice) return;
  const policy = installs ? (choice as MemberRemovalPolicy) : MemberRemovalPolicy.KeepAsDirectInstall;
  await ui.run(() => LibraryService.DeleteGroup(groupID, { if_no_other_install: policy }), { success: "已删除组" });
}

// ---------- 重新安装 ----------

const placeKey = (p: { skill_id: SkillID; scope: Scope }) => `${p.skill_id}|${scopeKey(p.scope)}`;

// reinstall 把 filter 范围内不是当前版本的各处换成当前版本。先现查状态：快照可能已落后于磁盘，
// 而重新安装只换列出的几处。被改过的几处逐处让用户勾选是否覆盖，没勾选的保持原样。
export async function reinstall(ui: UI, s: Snapshot, filter: SkillScopeFilter, what: string) {
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes(filter), { reload: false });
  if (inspected === undefined) return;
  const targets = (inspected ?? []).filter(needsReinstall);
  if (!targets.length) {
    ui.toast(`${what}已安装的都是当前版本`);
    return;
  }
  const modified = targets.filter((p) => p.status === SkillScopeStatus.SkillScopeModified);
  let overwrite = new Set<string>();
  if (modified.length) {
    const chosen = await ui.open<Set<string>>((close) => <OverwriteDialog s={s} places={modified} close={close} />);
    if (!chosen) return;
    overwrite = chosen;
  }
  const requests: ReinstallRequest[] = targets
    .filter((p) => p.status !== SkillScopeStatus.SkillScopeModified || overwrite.has(placeKey(p)))
    .map((p) => ({ skill_id: p.skill_id, scope: p.scope, overwrite: overwrite.has(placeKey(p)) }));
  const kept = targets.length - requests.length;
  const keptNote = kept ? `;${kept} 处改过的保持原样` : "";
  if (!requests.length) {
    ui.toast(`没有重新安装(${what})${keptNote}`);
    return;
  }
  const results = await ui.run(() => LibraryService.Reinstall(requests));
  if (!results) return;
  const failed = results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed);
  if (failed.length) {
    await showResults(ui, "部分未能重新安装", failed.map((r) => failedRow(s, r)), keptNote.slice(1) || undefined);
    return;
  }
  const done = results.filter((r) => r.outcome === ReinstallOutcome.Reinstalled).length;
  ui.toast(`已重新安装 ${done} 处(${what})${keptNote}`);
}

// OverwriteDialog 列出被改过的各处，默认都不覆盖;勾选的才用库里的当前版本覆盖。
function OverwriteDialog({
  s,
  places,
  close,
}: {
  s: Snapshot;
  places: SkillScopeInspection[];
  close: (value?: Set<string>) => void;
}) {
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const toggle = (key: string) =>
    setChosen((prev) => {
      const next = new Set(prev);
      if (!next.delete(key)) next.add(key);
      return next;
    });
  return (
    <Modal
      title="有技能文件夹被改过"
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>取消</Button>
          <Button variant={chosen.size ? "danger" : "primary"} onClick={() => close(chosen)} autoFocus>
            {chosen.size ? `重新安装并覆盖 ${chosen.size} 处` : "重新安装"}
          </Button>
        </>
      }
    >
      <Notice tone="warn">
        下面这些地方和库里任何版本都对不上，可能被手动改过。勾选的会被库里的当前版本覆盖，改动会丢掉;没勾选的保持原样。
      </Notice>
      <ListBox>
        {places.map((p) => {
          const key = placeKey(p);
          return (
            <ListRow key={key} asChild compact clickable selected={chosen.has(key)}>
              <label>
                <input type="checkbox" className="shrink-0 accent-primary" checked={chosen.has(key)} onChange={() => toggle(key)} />
                <div className="min-w-0 grow">
                  <div>
                    {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
                  </div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
                </div>
              </label>
            </ListRow>
          );
        })}
      </ListBox>
    </Modal>
  );
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

// checkUpdates 按集合检查更新(只读);没读成来源的集合列出原因。
export async function checkUpdates(ui: UI, s: Snapshot, collections: CollectionID[]) {
  const checks = await ui.run(() => LibraryService.CheckUpdates(collections));
  if (!checks) return;
  const skills = checks.flatMap((c) => c.skills ?? []);
  const available = skills.filter(updateAvailable).length;
  const deleted = skills.filter(deletedInSource).length;
  const summary = [
    `检查了 ${checks.length} 个来源`,
    available ? `${available} 个技能有新版本` : "",
    deleted ? `${deleted} 个技能来源里已删除` : "",
  ].filter(Boolean);
  const failed = checks.filter((c) => c.error);
  if (failed.length) {
    await showResults(
      ui,
      "检查更新",
      failed.map((c) => ({
        label: c.source?.label ? sourceLabel(s, c.source) : `集合 #${c.collection_id}`,
        outcome: "检查失败",
        tone: "bad" as const,
        reason: describeError(c.error!),
      })),
      [...summary, `${failed.length} 个来源没读成，可以稍后重试`].join(","),
    );
  } else {
    ui.toast(available || deleted ? summary.join(",") : `检查了 ${checks.length} 个来源，都是最新`);
  }
}

// openUpdate 打开集合的更新确认框;从技能发起时传 focus,默认只勾这一个。
export function openUpdate(ui: UI, s: Snapshot, collectionID: CollectionID, focus?: SkillID) {
  const collection = s.collections.find((c) => c.id === collectionID);
  if (!collection) return;
  return ui.open((close) => <UpdateDialog s={s} collection={collection} focus={focus} onClose={() => close()} />);
}

// previewCollection 对库里已有的集合再做一次入库预览，看作者又发布了哪些技能;已入库的候选标出来、不能再选。
export async function previewCollection(ui: UI, collectionID: CollectionID) {
  const preview = await ui.run(() => LibraryService.PreviewFetch({ collection: collectionID }), { reload: false });
  if (!preview) return;
  await ui.open((close) => <FetchReview preview={preview} audits={false} onClose={() => close()} onBack={() => close()} />);
}

// ---------- 移除技能 ----------

// lostEdits 说明删除这些文件夹时哪几处的改动会丢掉(状态取自快照里的巡检;共用目录的几个 Agent 共用一个文件夹，只算一处)。
const lostEdits = (places: SkillScopeInspection[]) => {
  const n = new Set(places.filter((p) => p.status === SkillScopeStatus.SkillScopeModified).map((p) => p.folder)).size;
  return n ? `;其中 ${n} 处被改过，改动会丢掉` : "";
};

export async function removeSkill(ui: UI, s: Snapshot, id: SkillID) {
  const places = skillScopesOf(s, id);
  const installed = places.length;
  const choice = await ui.confirm({
    title: `移除「${skillName(s, id)}」`,
    body: <Hint>从技能库删除它和全部历史版本，也会退出所有组。</Hint>,
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: "同时删除已安装的文件夹",
            desc: `从 ${installed} 个作用域的目录里移除文件夹${lostEdits(places)}`,
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

export async function relocateProject(ui: UI, project: RegisteredProject, path: string): Promise<boolean> {
  const moved = await ui.run(() => LibraryService.RelocateProject(project.id, path), {
    success: (p) => `已把项目「${p.name}」指到 ${p.path}`,
  });
  return !!moved;
}

export async function unregisterProject(ui: UI, s: Snapshot, project: RegisteredProject) {
  const places = s.skillScopes.filter((p) => p.scope.project_id === project.id);
  const installed = places.length;
  const choice = await ui.confirm({
    title: `注销项目「${projectName(s, project.id)}」`,
    body: (
      <Hint>
        {shortPath(s, project.path)} 不再由技能管理器管理，它在各 Agent 下的直接安装与组安装记录都会删除。
      </Hint>
    ),
    choices: installed
      ? [
          {
            value: FolderPolicy.Remove,
            label: "同时删除项目里已安装的文件夹",
            desc: `移除 ${new Set(places.map((p) => p.folder)).size} 个技能文件夹${lostEdits(places)}`,
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

// ---------- Agent ----------
// 只有自定义 Agent 能改、能删。它离开或删除时，旧目录里还有别的 Agent 在读的文件夹不动，确认框里逐个列出。

// SharedFolders 列出还有别的 Agent 在读、因而保留的技能文件夹。
function SharedFolders({ s, places }: { s: Snapshot; places: SkillScopeInspection[] }) {
  return (
    <ListBox>
      {places.map((p) => (
        <ListRow key={placeKey(p)}>
          <div className="min-w-0 grow">
            <div>
              {skillName(s, p.skill_id)} · {p.scope.project_id ? projectName(s, p.scope.project_id) : "全局"}
            </div>
            <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
          </div>
          <span className="text-[11px] text-muted-foreground">{(p.shared_with ?? []).map((code) => agentName(s, code)).join("、")} 在用</span>
        </ListRow>
      ))}
    </ListBox>
  );
}

export async function addAgent(ui: UI, input: CustomAgent, onInvalid: (err: LibraryError) => boolean): Promise<boolean> {
  const added = await ui.run(() => LibraryService.AddAgent(input), {
    success: (a) => `已添加 Agent「${a.name}」`,
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
  const globalChanged = cleanPath(input.global_directory) !== cleanPath(agent.global_directory);
  const projectChanged = cleanPath(input.project_directory) !== cleanPath(agent.project_directory);
  const changedHere = (scope: Scope) => scope.agent === agent.code && (placeOf(scope) ? projectChanged : globalChanged);
  const affected = s.skillScopes.filter((p) => changedHere(p.scope));
  const groupInstalls = s.groupInstalls.filter((g) => changedHere(g.scope)).length;
  let migrate = false;
  if (affected.length || groupInstalls) {
    const shared = affected.filter((p) => p.shared_with?.length);
    const choice = await ui.confirm({
      title: `改 Agent「${agent.name}」的目录`,
      body: (
        <>
          <Hint>
            这个 Agent 在旧目录里{[affected.length && `已装了 ${affected.length} 处技能`, groupInstalls && `有 ${groupInstalls} 个组安装`].filter(Boolean).join(",")}。
          </Hint>
          {shared.length > 0 && (
            <>
              <Notice>
                其中 {shared.length} 个文件夹还有别的 Agent 在用，留在旧目录给它们：迁移时复制一份到新目录，不迁移时不动。
              </Notice>
              <SharedFolders s={s} places={shared} />
            </>
          )}
        </>
      ),
      choices: [
        {
          value: "migrate",
          label: "迁移到新目录",
          desc: "把已装的文件夹原样搬过去，组安装保留;新目录里已有同名文件夹的不覆盖、不搬，旧文件夹留在原处，完成后列出",
        },
        { value: "keep", label: "不迁移", desc: "只改目录，旧目录里的文件夹不动，去掉这个 Agent 在那里的安装记录与组安装" },
      ],
      confirm: "保存",
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
      `已保存 Agent「${name}」`,
      skipped.map((m) => ({
        label: `${skillName(s, m.skill_id)} · ${m.scope.project_id ? projectName(s, m.scope.project_id) : "全局"}`,
        outcome: "没有迁移",
        tone: "warn" as const,
        reason: `新目录里已有同名文件夹，或被库里另一个技能的安装占用:${shortPath(s, m.to)}\n旧文件夹留在原处:${shortPath(s, m.from)}`,
      })),
      `迁移了 ${migrations.length - skipped.length} 个文件夹;下面 ${skipped.length} 个没有迁移，这个 Agent 在那里不再算已装。`,
    );
  } else {
    ui.toast(migrations.length ? `已保存 Agent「${name}」,迁移了 ${migrations.length} 个文件夹` : `已保存 Agent「${name}」`);
  }
  return true;
}

// deleteAgent 删除自定义 Agent,和注销项目一样让用户选文件夹的去留;还有别的 Agent 在读的文件夹不管怎么选都保留。
export async function deleteAgent(ui: UI, s: Snapshot, agent: Agent) {
  const places = s.skillScopes.filter((p) => p.scope.agent === agent.code);
  const kept = places.filter((p) => p.shared_with?.length);
  const own = places.filter((p) => !p.shared_with?.length);
  const choice = await ui.confirm({
    title: `删除 Agent「${agent.name}」`,
    body: (
      <>
        <Hint>它从 Agent 列表里消失，在全局和各项目的直接安装与组安装记录都会删除。</Hint>
        {kept.length > 0 && (
          <>
            <Notice>
              下面 {kept.length} 个文件夹还有别的 Agent 在用，不管怎么选都保留，安装记录转给它们。
            </Notice>
            <SharedFolders s={s} places={kept} />
          </>
        )}
      </>
    ),
    choices: own.length
      ? [
          {
            value: FolderPolicy.Remove,
            label: "同时删除已安装的文件夹",
            desc: `移除 ${own.length} 个技能文件夹${lostEdits(own)}`,
          },
          { value: FolderPolicy.Keep, label: "保留已安装的文件夹", desc: "文件夹留在原处，技能库从此不再管" },
        ]
      : undefined,
    confirm: "删除",
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await ui.run(() => LibraryService.DeleteAgent(agent.code, { folders: policy }), {
    success: `已删除 Agent「${agent.name}」`,
  });
}
