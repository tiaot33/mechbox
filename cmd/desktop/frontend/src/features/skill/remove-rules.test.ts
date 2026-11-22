// 移除技能：一批移除涉及几个作用域，移除同名的另一个时能在哪几处换上留下的那个，换装后哪些技能还会找不到它提到的技能。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, SkillScopeStatus, SourceType, type Agent, type MissingMention, type Scope, type Skill, type SkillScopeInspection } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { lostAfterTwin, removedPlaces, twinReplaceScopes } from "./remove-rules";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const place = (skill_id: number, scope: Scope): SkillScopeInspection => ({ skill_id, scope, directory: "", folder: `/f${skill_id}`, direct: true, status: SkillScopeStatus.SkillScopeCurrent });

// 项目里 Claude Code 和 Codex 读同一个目录：合成一处。
const snapshot = (skillScopes: SkillScopeInspection[] = []) =>
  ({
    skillScopes,
    agents: [{ code: claude, name: "Claude Code", project_directories: [".agents" ]} as Agent, { code: codex, name: "Codex", project_directories: [".agents" ]} as Agent],
  }) as unknown as Snapshot;

describe("一批移除涉及几个作用域", () => {
  it("几个技能装在同一个作用域只算一个，项目里共用一个目录的几个 Agent 是一个", () => {
    const s = snapshot([place(1, global(claude)), place(2, global(claude)), place(1, project(claude, 7)), place(2, project(codex, 7)), place(3, global(codex))]);
    const r = removedPlaces(s, [1, 2]);
    expect(r.places).toHaveLength(4);
    expect(r.scopes).toBe(2);
    expect(removedPlaces(s, [9])).toEqual({ places: [], scopes: 0 });
  });
});

describe("移除同名的另一个之后还会找不到的", () => {
  const mention = (scope: Scope, name: string) => ({ skill_id: 5, scope, name }) as MissingMention;

  it("在那几处装上留下的这个时，那几处提到这个名字的不再算;别处的、提到别的名字的照算", () => {
    const s = snapshot();
    const unfound = [mention(project(codex, 7), "x"), mention(global(claude), "x"), mention(project(claude, 7), "y")];
    expect(lostAfterTwin(s, unfound, "x", [project(claude, 7)])).toEqual([unfound[1], unfound[2]]);
    expect(lostAfterTwin(s, unfound, "x", [])).toEqual(unfound);
  });
});

describe("移除同名的另一个时能在哪几处换上留下的", () => {
  const snapshot = (s: Partial<Snapshot>) =>
    ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;
  const skill = (id: number, name: string, label: string) => ({ id, name, source: { type: SourceType.SourceGitHub, label } }) as Skill;
  const where = (agent: string, project_id?: number) =>
    project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
  const installed = (skill_id: number, agent: string, project_id?: number) => ({ skill_id, scope: where(agent, project_id) }) as SkillScopeInspection;
  // 两个 tdd:A(1) 装在 Claude Code 全局;B(2) 装在 Codex 全局、Claude Code 在项目 7、Codex 在项目 9,和 Claude Code 在项目 8(不在这台机器上)。
  const s = snapshot({
    skills: [skill(1, "tdd", "a/skills"), skill(2, "tdd", "b/skills")],
    skillScopes: [
      installed(1, "claude-code"),
      installed(2, "codex"),
      installed(2, "claude-code", 7),
      installed(2, "codex", 9),
      { ...installed(2, "claude-code", 8), status: SkillScopeStatus.SkillScopeRootMissing },
    ],
  });

  it("移除 B 留下 A 时，能换上 A 的是 B 装着、项目在这台机器上、那个 Agent 在全局没装 A 的几处", () => {
    // Claude Code 在项目 7 里本来就用得到全局的 A,不再装一份。
    expect(twinReplaceScopes(s, 2, 1)).toEqual([where("codex"), where("codex", 9)]);
    expect(twinReplaceScopes(s, 1, 2)).toEqual([where("claude-code")]);
  });

  it("没装在任何地方时没有要换的", () => {
    expect(twinReplaceScopes(snapshot({ skills: s.skills }), 1, 2)).toEqual([]);
  });
});
