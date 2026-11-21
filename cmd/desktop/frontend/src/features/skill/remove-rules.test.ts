// 移除技能：一批移除涉及几个作用域，移除同名的另一个并在那几处换装后，哪些技能还会找不到它提到的技能。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, SkillScopeStatus, type Agent, type MissingMention, type Scope, type SkillScopeInspection } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { lostAfterTwin, removedPlaces } from "./remove-rules";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const place = (skill_id: number, scope: Scope): SkillScopeInspection => ({ skill_id, scope, folder: `/f${skill_id}`, direct: true, status: SkillScopeStatus.SkillScopeCurrent });

// 项目里 Claude Code 和 Codex 读同一个目录：合成一处。
const snapshot = (skillScopes: SkillScopeInspection[] = []) =>
  ({
    skillScopes,
    agents: [{ code: claude, name: "Claude Code", project_directory: ".agents" } as Agent, { code: codex, name: "Codex", project_directory: ".agents" } as Agent],
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
