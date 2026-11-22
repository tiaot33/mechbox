// 作用域在界面里的身份(ScopeKey)、「地方」(全局或一个已登记项目),以及 Agent、项目、技能、组的名字。
// 作用域在界面上叫什么(项目里共用一个目录的几个 Agent 合成一处)见 lib/places。
import { ScopeKind, type AgentCode, type GroupID, type ProjectID, type Scope, type SkillID } from "@bindings/internal/library";
import type { Snapshot } from "./library";

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

export function projectName(s: Pick<Snapshot, "projects">, id: ProjectID): string {
  return s.projects.find((p) => p.id === id)?.name ?? "";
}

// skillName / groupName 是库里技能、组的名字;已被移除的写成 #ID。
export const skillName = (s: Snapshot, id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;

export const groupName = (s: Snapshot, id: GroupID) => s.groups.find((g) => g.id === id)?.name ?? `#${id}`;
