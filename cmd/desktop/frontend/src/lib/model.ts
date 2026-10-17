// 主界面的领域辅助：作用域身份、来源展示、安装状态与筛选，都从只读快照派生。
import {
  SameNameRule,
  ScopeKind,
  SkillScopeStatus,
  SourceType,
  UpdateCheckStatus,
  type Agent,
  type AgentCode,
  type Collection,
  type CollectionID,
  type Group,
  type GroupID,
  type ProjectID,
  type Scope,
  type Skill,
  type SkillScopeInspection,
  type Source,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import type { Snapshot } from "./library";

// ---------- 作用域 ----------

// ScopeKey 是作用域在界面状态里的身份，形如 "claude-code@0";0 表示 Agent 的全局级别。
export type ScopeKey = string;

// Place 是"地方":0 = 全局，否则为已登记项目 ID。
export type Place = number;

export function scopeKey(scope: { agent: AgentCode; project_id?: ProjectID }): ScopeKey {
  return `${scope.agent}@${scope.project_id ?? 0}`;
}

export function scopeOf(agent: AgentCode, place: Place): Scope {
  return place ? { kind: ScopeKind.ScopeProject, agent, project_id: place } : { kind: ScopeKind.ScopeGlobal, agent };
}

export function parseScope(key: ScopeKey): Scope {
  const at = key.lastIndexOf("@");
  return scopeOf(key.slice(0, at) as AgentCode, Number(key.slice(at + 1)));
}

export function placeOf(scope: Scope): Place {
  return scope.project_id ?? 0;
}

export function agentName(s: Snapshot, code: AgentCode): string {
  return s.agents.find((a) => a.code === code)?.name ?? code;
}

export function projectName(s: Snapshot, id: ProjectID): string {
  return s.projects.find((p) => p.id === id)?.name ?? "";
}

export function scopeLabel(s: Snapshot, scope: Scope): string {
  return `${agentName(s, scope.agent)} · ${scope.project_id ? projectName(s, scope.project_id) : "全局"}`;
}

// shortPath 把用户主目录缩写成 ~。
export function shortPath(s: Snapshot, path: string): string {
  const home = s.info.home_dir;
  return home && (path === home || path.startsWith(home + "/")) ? "~" + path.slice(home.length) : path;
}

// agentShown 是 Agent 的显示勾选：用户勾选过的按勾选，没勾选过的检测到的和自定义的显示。
// 显示只影响选择安装位置的地方(右栏与胶囊弹层的"其他 Agent"、"安装到…");已经装了技能的 Agent 照常列出。
export function agentShown(shown: Partial<Record<string, boolean>> | null | undefined, agent: Agent): boolean {
  return shown?.[agent.code] ?? (agent.detected || agent.custom);
}

// agentsIn 把某个地方的 Agent 分成直接列出的和折进"其他 N 个 Agent"的(右栏与作用域弹层):
// 在这里装有技能的、以及检测到且勾选显示的直接列出;勾选了显示但没检测到的折起来;没勾选显示、也没装技能的不列出。
export function agentsIn(s: Snapshot, place: Place): { open: Agent[]; folded: Agent[] } {
  const used = agentsUsedIn(s, place);
  const open = s.agents.filter((a) => used.includes(a) || (a.detected && agentShown(s.agentsShown, a)));
  const folded = s.agents.filter((a) => !open.includes(a) && agentShown(s.agentsShown, a));
  return { open, folded };
}

// agentsUsedIn 是某个地方实际装有技能的 Agent,按 Agent 列表顺序。
export function agentsUsedIn(s: Snapshot, place: Place) {
  return s.agents.filter((a) => s.skillScopes.some((p) => p.scope.agent === a.code && placeOf(p.scope) === place));
}

// ---------- 安装 ----------

export function skillScopesOf(s: Snapshot, skillID: number): SkillScopeInspection[] {
  return s.skillScopes.filter((p) => p.skill_id === skillID);
}

export function skillScopesIn(s: Snapshot, key: ScopeKey): SkillScopeInspection[] {
  return s.skillScopes.filter((p) => scopeKey(p.scope) === key);
}

// unmanagedIn 是作用域的技能目录里不是库装的技能文件夹(未纳管技能)。
export function unmanagedIn(s: Snapshot, key: ScopeKey): UnmanagedSkill[] {
  return s.unmanaged.filter((u) => scopeKey(u.scope) === key);
}

// sameNameNote 说明作用域里的一个技能文件夹和同一个 Agent 全局 / 某个已登记项目里的文件夹同名时会怎样:
// 按 Agent 已知的规则说出后果(Claude Code 里全局的生效，Codex 里两份都会出现),没有已知规则的只说同名。
// folder 是这个作用域里那个技能文件夹;没有同名时返回 undefined。title 列出另一处的文件夹。
export function sameNameNote(s: Snapshot, key: ScopeKey, folder: string): { text: string; title: string } | undefined {
  const scope = parseScope(key);
  const agent = s.agents.find((a) => a.code === scope.agent);
  const rule = agent?.same_name ?? SameNameRule.SameNameUnknown;
  const who = agent?.name ?? scope.agent;
  if (scope.project_id) {
    const pair = s.sameName.find((p) => p.agent === scope.agent && p.project_id === scope.project_id && p.project.folder === folder);
    if (!pair) return undefined;
    const other = `${pair.name}(${pair.global.skill_id ? "库装的" : "未纳管"})`;
    const text = {
      [SameNameRule.SameNameGlobalWins]: `全局也有 ${other}，${who} 用全局的那个，这里的不会生效`,
      [SameNameRule.SameNameBothShown]: `全局也有 ${other}，${who} 里两份都会出现`,
      [SameNameRule.SameNameUnknown]: `全局也有同名的 ${other}`,
    }[rule];
    return { text, title: `全局的文件夹:${shortPath(s, pair.global.folder)}` };
  }
  const pairs = s.sameName.filter((p) => p.agent === scope.agent && p.global.folder === folder);
  if (pairs.length === 0) return undefined;
  const name = pairs[0].name;
  const projects = `项目「${pairs.map((p) => projectName(s, p.project_id)).join("」「")}」`;
  const text = {
    [SameNameRule.SameNameGlobalWins]: `${projects}里也有 ${name}，${who} 在那里用全局的这个`,
    [SameNameRule.SameNameBothShown]: `${projects}里也有 ${name}，${who} 在那里两份都会出现`,
    [SameNameRule.SameNameUnknown]: `${projects}里也有同名的 ${name}`,
  }[rule];
  return { text, title: pairs.map((p) => `「${projectName(s, p.project_id)}」的文件夹:${shortPath(s, p.project.folder)}`).join("\n") };
}

export function skillScopeIn(s: Snapshot, skillID: number, key: ScopeKey): SkillScopeInspection | undefined {
  return s.skillScopes.find((p) => p.skill_id === skillID && scopeKey(p.scope) === key);
}

export function needsReinstall(p: SkillScopeInspection): boolean {
  return p.status !== SkillScopeStatus.SkillScopeCurrent;
}

export const skillScopeStatus: Record<SkillScopeStatus, { label: string; tone: "ok" | "warn" | "bad" }> = {
  [SkillScopeStatus.SkillScopeCurrent]: { label: "最新", tone: "ok" },
  [SkillScopeStatus.SkillScopeStale]: { label: "旧版本", tone: "warn" },
  [SkillScopeStatus.SkillScopeModified]: { label: "被手动改过", tone: "bad" },
  [SkillScopeStatus.SkillScopeMissing]: { label: "文件夹不在了", tone: "bad" },
  [SkillScopeStatus.SkillScopeRootMissing]: { label: "项目不在这台机器上", tone: "bad" },
  [SkillScopeStatus.$zero]: { label: "未知", tone: "warn" },
};

// groupStateIn 是组在某个作用域的组安装状态：组安装是独立记录(空组也可以有),有记录即为 on。
// 成员各自的直接安装不算组安装。
export function groupStateIn(s: Snapshot, group: Group, key: ScopeKey): "on" | "off" {
  return s.groupInstalls.some((a) => a.group_id === group.id && scopeKey(a.scope) === key) ? "on" : "off";
}

export function viaGroupCount(s: Snapshot, group: Group, key: ScopeKey): number {
  return (group.members ?? []).filter((id) => skillScopeIn(s, id, key)?.groups?.includes(group.id)).length;
}

// ---------- 更新 ----------
// 计入角标的规则由技能库给出(UpdateCheck.pending):有新版本且没跳过、来源里已删除且没选继续保留、推测出改名且没跳过;
// 本地目录来源、跳过的改动、来源里的新技能都不计入。侧边栏的更新计数、更新列表和 Dock 角标都按它算。
// 本地目录来源的作者就是用户自己：不进更新列表，技能上只有"重新读取"。

export function isLocal(source: Source): boolean {
  return source.type === SourceType.SourceLocal;
}

// noSource 是没有来源的技能：从作用域入库的未纳管技能，作者就是用户自己。不在任何集合里，不检查更新，
// 只能用目录里的修改更新(在装出去的地方改了以后，那一处会出现"用这里的修改更新")。
export function noSource(source: Source): boolean {
  return source.type === SourceType.$zero;
}

export function pending(skill: Skill): boolean {
  return !!skill.last_update_check?.pending;
}

// updateAvailable 是来源里这个技能有新版本，跳过了的也算(更新计划里照样能勾上)。
export function updateAvailable(skill: Skill): boolean {
  return !isLocal(skill.source) && skill.last_update_check?.status === UpdateCheckStatus.UpdateAvailable;
}

// renamedTo 是推测这个技能在来源里改成的新名字;没有推测时为空。
export function renamedTo(skill: Skill): string {
  return skill.last_update_check?.renamed_to ?? "";
}

// deletedInSource 是来源里已删除、也没推测出改名。
export function deletedInSource(skill: Skill): boolean {
  return skill.last_update_check?.status === UpdateCheckStatus.UpdateDeleted && !renamedTo(skill);
}

// hasUpdate 是能从这个技能打开更新计划：有新版本或推测出改名，跳过了的也算。
export function hasUpdate(skill: Skill): boolean {
  return updateAvailable(skill) || !!renamedTo(skill);
}

export function collectionSkills(s: Snapshot, c: Collection): Skill[] {
  const ids = new Set(c.skills ?? []);
  return s.skills.filter((x) => ids.has(x.id));
}

// updatableCollections 是有计入角标的技能的集合，按集合列表顺序。
export function updatableCollections(s: Snapshot): Collection[] {
  return s.collections.filter((c) => collectionSkills(s, c).some(pending));
}

// checkableCollections 是检查更新的范围：本地目录来源不检查。
export function checkableCollections(s: Snapshot): CollectionID[] {
  return s.collections.filter((c) => !isLocal(c.source)).map((c) => c.id);
}

export function shortRevision(revision: string): string {
  return revision.slice(0, 7);
}

// revisionSummary 说明集合里的技能在哪个来源版本上:都在同一个时"在 a1b2c3";
// 不在同一个时直接写"pdf 在 d4e5f6,其余 4 个在 a1b2c3",不起专门的名字。本地目录来源为空。
export function revisionSummary(skills: Skill[]): string {
  const byRevision = new Map<string, Skill[]>();
  for (const x of skills) {
    if (!x.revision) continue;
    const key = shortRevision(x.revision);
    byRevision.set(key, [...(byRevision.get(key) ?? []), x]);
  }
  if (byRevision.size === 0) return "";
  const groups = [...byRevision.entries()].sort((a, b) => b[1].length - a[1].length);
  const [main, ...others] = groups;
  if (!others.length) return `在 ${main[0]}`;
  const parts = others.map(([rev, list]) => `${list.map((x) => x.name).join("、")} 在 ${rev}`);
  return `${parts.join(",")},其余 ${main[1].length} 个在 ${main[0]}`;
}

// ---------- 来源 ----------

export const sourceKindLabel: Record<SourceType, string> = {
  [SourceType.SourceGitHub]: "GitHub",
  [SourceType.SourceGit]: "Git",
  [SourceType.SourceURL]: "URL",
  [SourceType.SourceWellKnown]: "well-known",
  [SourceType.SourceLocal]: "自研",
  [SourceType.$zero]: "没有来源",
};

function lastSegment(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

// sourceLabel 是集合在左栏里的名字：GitHub 取 owner/repo,URL 取文件名，本地取缩写路径。
export function sourceLabel(s: Snapshot, source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
      return source.label;
    case SourceType.SourceURL:
      return lastSegment(source.label);
    case SourceType.SourceLocal:
      return shortPath(s, source.label);
    case SourceType.$zero:
      return "没有来源";
    default:
      return "未知来源";
  }
}

// sourceShort 是卡片上的来源短名：GitHub 取 owner,URL 取文件名，本地一律"自研"。
export function sourceShort(s: Snapshot, source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
      return sourceLabel(s, source).split("/")[0];
    case SourceType.SourceURL:
      return sourceLabel(s, source).replace(/\.zip$/, "");
    default:
      return sourceKindLabel[source.type];
  }
}

// ---------- 左栏筛选 ----------

export type SkillFilter =
  | { kind: "all" }
  | { kind: "updates" }
  | { kind: "collection"; collectionID: CollectionID }
  | { kind: "group"; groupID: GroupID };

export function sameFilter(a: SkillFilter, b: SkillFilter): boolean {
  if (a.kind === "collection" && b.kind === "collection") return a.collectionID === b.collectionID;
  if (a.kind === "group" && b.kind === "group") return a.groupID === b.groupID;
  return a.kind === b.kind;
}

// resolveFilter 把指向已不存在的集合或组、或已没有更新的筛选退回"所有技能"。
export function resolveFilter(s: Snapshot, filter: SkillFilter): SkillFilter {
  if (filter.kind === "collection" && !s.collections.some((c) => c.id === filter.collectionID)) return { kind: "all" };
  if (filter.kind === "group" && !s.groups.some((g) => g.id === filter.groupID)) return { kind: "all" };
  if (filter.kind === "updates" && !updatableCollections(s).length) return { kind: "all" };
  return filter;
}

export function filterTitle(s: Snapshot, filter: SkillFilter): string {
  switch (filter.kind) {
    case "collection": {
      const c = s.collections.find((c) => c.id === filter.collectionID);
      return c ? sourceLabel(s, c.source) : "所有技能";
    }
    case "group":
      return s.groups.find((g) => g.id === filter.groupID)?.name ?? "所有技能";
    case "updates":
      return "更新";
    default:
      return "所有技能";
  }
}

export interface ListQuery {
  filter: SkillFilter;
  search: string;
  installedOnlyIn: ScopeKey | null; // 仅显示已安装在该作用域的技能
}

export function visibleSkills(s: Snapshot, q: ListQuery): Skill[] {
  let skills = s.skills;
  const f = q.filter;
  if (f.kind === "collection") {
    const ids = new Set(s.collections.find((c) => c.id === f.collectionID)?.skills ?? []);
    skills = skills.filter((x) => ids.has(x.id));
  }
  if (f.kind === "group") {
    const ids = new Set(s.groups.find((g) => g.id === f.groupID)?.members ?? []);
    skills = skills.filter((x) => ids.has(x.id));
  }
  if (f.kind === "updates") skills = skills.filter(pending);
  const text = q.search.trim().toLowerCase();
  if (text) skills = skills.filter((x) => (x.name + " " + x.description).toLowerCase().includes(text));
  const key = q.installedOnlyIn;
  if (key) skills = skills.filter((x) => skillScopeIn(s, x.id, key));
  return skills;
}

// visibleUnmanaged 是选中作用域、看"所有技能"时和库里的技能列在一起的未纳管技能;按组、集合、更新筛选时不列。
export function visibleUnmanaged(s: Snapshot, filter: SkillFilter, search: string, scope: ScopeKey | null): UnmanagedSkill[] {
  if (!scope || filter.kind !== "all") return [];
  const text = search.trim().toLowerCase();
  return unmanagedIn(s, scope).filter((u) => (u.name + " " + (u.description ?? "")).toLowerCase().includes(text));
}
