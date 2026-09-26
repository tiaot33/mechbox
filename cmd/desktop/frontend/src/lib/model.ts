// 主界面的领域辅助：作用域身份、来源展示、激活状态与筛选，都从只读快照派生。
import {
  ProjectionStatus,
  ScopeKind,
  SourceType,
  UpdateCheckStatus,
  type AgentCode,
  type GroupWithMembers,
  type ProjectID,
  type ProjectionInspection,
  type Scope,
  type SkillSummary,
  type Source,
  type SourceID,
  type GroupID,
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
  const path = s.projects.find((p) => p.id === id)?.path ?? "";
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

export function scopeLabel(s: Snapshot, scope: Scope): string {
  return `${agentName(s, scope.agent)} · ${scope.project_id ? projectName(s, scope.project_id) : "全局"}`;
}

// shortPath 把用户主目录缩写成 ~。
export function shortPath(s: Snapshot, path: string): string {
  const home = s.info.home_dir;
  return home && (path === home || path.startsWith(home + "/")) ? "~" + path.slice(home.length) : path;
}

// agentsUsedIn 是某个地方实际有投影的 Agent,按 Agent 列表顺序。
export function agentsUsedIn(s: Snapshot, place: Place) {
  return s.agents.filter((a) => s.projections.some((p) => p.scope.agent === a.code && placeOf(p.scope) === place));
}

// ---------- 投影与激活 ----------

export function projectionsOf(s: Snapshot, skillID: number): ProjectionInspection[] {
  return s.projections.filter((p) => p.skill_id === skillID);
}

export function projectionsIn(s: Snapshot, key: ScopeKey): ProjectionInspection[] {
  return s.projections.filter((p) => scopeKey(p.scope) === key);
}

export function projectionIn(s: Snapshot, skillID: number, key: ScopeKey): ProjectionInspection | undefined {
  return s.projections.find((p) => p.skill_id === skillID && scopeKey(p.scope) === key);
}

export function needsRewrite(p: ProjectionInspection): boolean {
  return p.status !== ProjectionStatus.ProjectionCurrent;
}

export const projectionStatus: Record<ProjectionStatus, { label: string; tone: "ok" | "warn" | "bad" }> = {
  [ProjectionStatus.ProjectionCurrent]: { label: "最新", tone: "ok" },
  [ProjectionStatus.ProjectionStale]: { label: "旧版本", tone: "warn" },
  [ProjectionStatus.ProjectionModified]: { label: "已被改动", tone: "bad" },
  [ProjectionStatus.ProjectionMissing]: { label: "目录缺失", tone: "bad" },
  [ProjectionStatus.ProjectionDirectoryMissing]: { label: "作用域目录缺失", tone: "bad" },
  [ProjectionStatus.$zero]: { label: "未知", tone: "warn" },
};

// groupStateIn 是组在某个作用域的整组激活状态：组激活是独立记录(空组也可以有),有记录即为 on。
// 成员各自的独立激活不算整组激活。
export function groupStateIn(s: Snapshot, group: GroupWithMembers, key: ScopeKey): "on" | "off" {
  return s.groupActivations.some((a) => a.group_id === group.group.id && scopeKey(a.scope) === key) ? "on" : "off";
}

export function viaGroupCount(s: Snapshot, group: GroupWithMembers, key: ScopeKey): number {
  return (group.members ?? []).filter((id) => projectionIn(s, id, key)?.groups?.includes(group.group.id)).length;
}

export function updateAvailable(skill: SkillSummary): boolean {
  return skill.last_update_check?.status === UpdateCheckStatus.UpdateAvailable;
}

// ---------- 来源 ----------

export const sourceKindLabel: Record<SourceType, string> = {
  [SourceType.SourceGitHub]: "GitHub",
  [SourceType.SourceURL]: "URL",
  [SourceType.SourceStore]: "商店",
  [SourceType.SourceLocal]: "自研",
  [SourceType.$zero]: "未知",
};

function lastSegment(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

// sourceLabel 是集合在左栏里的名字：GitHub 取 owner/repo,商店取包 ID,URL 取文件名，本地取缩写路径。
export function sourceLabel(s: Snapshot, source: Source): string {
  const l = source.locator;
  switch (source.type) {
    case SourceType.SourceGitHub:
      return (l.repository ?? "").replace(/^https?:\/\/github\.com\//, "").replace(/\.git$/, "");
    case SourceType.SourceStore:
      return l.package_id ?? "";
    case SourceType.SourceURL:
      return lastSegment(l.url ?? "");
    case SourceType.SourceLocal:
      return shortPath(s, l.path ?? "");
    default:
      return "未知来源";
  }
}

// sourceShort 是卡片上的来源短名：GitHub 取 owner,商店取包名末段,URL 取文件名，本地一律"自研"。
export function sourceShort(s: Snapshot, source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
      return sourceLabel(s, source).split("/")[0];
    case SourceType.SourceStore:
      return lastSegment(sourceLabel(s, source));
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
  | { kind: "collection"; sourceID: SourceID }
  | { kind: "group"; groupID: GroupID };

export function sameFilter(a: SkillFilter, b: SkillFilter): boolean {
  if (a.kind === "collection" && b.kind === "collection") return a.sourceID === b.sourceID;
  if (a.kind === "group" && b.kind === "group") return a.groupID === b.groupID;
  return a.kind === b.kind;
}

// resolveFilter 把指向已不存在的集合或组的筛选退回"所有技能"。
export function resolveFilter(s: Snapshot, filter: SkillFilter): SkillFilter {
  if (filter.kind === "collection" && !s.collections.some((c) => c.source.id === filter.sourceID)) return { kind: "all" };
  if (filter.kind === "group" && !s.groups.some((g) => g.group.id === filter.groupID)) return { kind: "all" };
  return filter;
}

export function filterTitle(s: Snapshot, filter: SkillFilter): string {
  switch (filter.kind) {
    case "collection": {
      const c = s.collections.find((c) => c.source.id === filter.sourceID);
      return c ? sourceLabel(s, c.source) : "所有技能";
    }
    case "group":
      return s.groups.find((g) => g.group.id === filter.groupID)?.group.name ?? "所有技能";
    case "updates":
      return "有可用更新";
    default:
      return "所有技能";
  }
}

export interface ListQuery {
  filter: SkillFilter;
  search: string;
  activeOnlyIn: ScopeKey | null; // 仅显示在该作用域已激活的技能
}

export function visibleSkills(s: Snapshot, q: ListQuery): SkillSummary[] {
  let skills = s.skills;
  const f = q.filter;
  if (f.kind === "collection") {
    const ids = new Set((s.collections.find((c) => c.source.id === f.sourceID)?.skills ?? []).map((x) => x.id));
    skills = skills.filter((x) => ids.has(x.id));
  }
  if (f.kind === "group") {
    const ids = new Set(s.groups.find((g) => g.group.id === f.groupID)?.members ?? []);
    skills = skills.filter((x) => ids.has(x.id));
  }
  if (f.kind === "updates") skills = skills.filter(updateAvailable);
  const text = q.search.trim().toLowerCase();
  if (text) skills = skills.filter((x) => (x.name + " " + x.description).toLowerCase().includes(text));
  const key = q.activeOnlyIn;
  if (key) skills = skills.filter((x) => projectionIn(s, x.id, key));
  return skills;
}
