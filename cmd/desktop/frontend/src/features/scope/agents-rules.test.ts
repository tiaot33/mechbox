// 自定义 Agent:改目录时哪些已装的技能、组安装受影响，删 Agent 时哪些文件夹归它。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ScopeKind,
  SkillScopeStatus,
  type Agent,
  type CustomAgent,
  type GroupInstall,
  type Scope,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { agentDelete, agentEdit } from "./agents-rules";

const mine = "my-agent" as AgentCode;
const claude = AgentCode.AgentClaudeCode;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const place = (skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}): SkillScopeInspection => ({
  skill_id,
  scope,
  folder: `/${scope.project_id ?? "home"}/${scope.agent}/s${skill_id}`,
  direct: true,
  status: SkillScopeStatus.SkillScopeCurrent,
  ...extra,
});

const agent = { code: mine, name: "Mine", global_directory: "/home/.mine/skills", project_directory: ".mine/skills" } as Agent;
const input = (extra: Partial<CustomAgent>) => ({ name: "Mine", global_directory: "/home/.mine/skills", project_directory: ".mine/skills", ...extra }) as CustomAgent;

const snapshot = (skillScopes: SkillScopeInspection[], groupInstalls: GroupInstall[] = []) => ({ skillScopes, groupInstalls }) as unknown as Snapshot;

describe("改自定义 Agent 的目录", () => {
  const s = snapshot(
    [place(1, global(mine), { shared_with: [claude] }), place(2, global(mine), { linked_by: [global(claude)] }), place(3, project(mine, 7)), place(4, global(claude))],
    [{ group_id: 5, scope: project(mine, 7) } as GroupInstall],
  );

  it("写法不同的同一个目录不算改了，不问", () => {
    expect(agentEdit(s, agent, input({ global_directory: "/home/.mine/skills/", project_directory: "./.mine/skills" }))).toMatchObject({ affected: [], groupInstalls: 0, asks: false });
  });

  it("只算改了的那一层：全局改了算全局装的;还有别的 Agent 在读的另说，别处链接指着要搬走的先换成独立的一份", () => {
    const r = agentEdit(s, agent, input({ global_directory: "/home/.other" }));
    expect(r.affected.map((p) => p.skill_id)).toEqual([1, 2]);
    expect(r.groupInstalls).toBe(0);
    expect(r.asks).toBe(true);
    expect(r.shared.map((p) => p.skill_id)).toEqual([1]);
    expect(r.detached.map((d) => d.place.skill_id)).toEqual([2]);
  });

  it("项目目录改了算各项目里装的和组安装;只有组安装时也问", () => {
    const r = agentEdit(s, agent, input({ project_directory: ".other" }));
    expect(r.affected.map((p) => p.skill_id)).toEqual([3]);
    expect(r.groupInstalls).toBe(1);
    expect(agentEdit(snapshot([], s.groupInstalls), agent, input({ project_directory: ".other" })).asks).toBe(true);
  });
});

describe("删除自定义 Agent", () => {
  it("只有它在读的文件夹归它;还有别的 Agent 在读的保留，别的 Agent 的不算", () => {
    const r = agentDelete(snapshot([place(1, global(mine), { shared_with: [claude] }), place(2, global(mine)), place(3, global(claude))]), mine);
    expect(r.kept.map((p) => p.skill_id)).toEqual([1]);
    expect(r.own.map((p) => p.skill_id)).toEqual([2]);
  });
});
