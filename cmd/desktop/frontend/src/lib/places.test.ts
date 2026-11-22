// 界面上的「一处」:项目里共用一个目录的几个 Agent 合成一处，写名字、数个数、逐处列出时都只算一次;全局不合。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, type Agent, type RegisteredProject, type Scope } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { eachPlace, mergedScope, mergedScopeKey, placeCount, placeNames, sameMergedScope, scopeCount } from "./places";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const cursor = "cursor" as AgentCode;
const at = (agent: AgentCode, project_id = 0): Scope => (project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent });
// Codex 和 Cursor 在项目里都读 .agents/skills,全局目录各是各的。
const s = {
  agents: [
    { code: claude, name: "Claude Code", global_directory: "/home/.claude/skills", project_directory: ".claude/skills", detected: true },
    { code: codex, name: "Codex", global_directory: "/home/.codex/skills", project_directory: ".agents/skills", detected: true },
    { code: cursor, name: "Cursor", global_directory: "/home/.cursor/skills", project_directory: ".agents/skills", detected: true },
  ] as Agent[],
  agentsShown: {},
  projects: [{ id: 1, name: "web" }] as RegisteredProject[],
} as unknown as Snapshot;
const placed = (skill_id: number, scope: Scope) => ({ skill_id, scope });

describe("几处", () => {
  // 技能 1 装在 Codex 全局、Cursor 全局，和项目 web 里共用目录的 Codex、Cursor;技能 2 只装在项目 web 的 Cursor。
  const installs = [placed(1, at(codex)), placed(1, at(cursor)), placed(1, at(codex, 1)), placed(1, at(cursor, 1)), placed(2, at(cursor, 1))];

  it("项目里共用一个目录的几个 Agent 是一处，全局照旧一个 Agent 一处", () => {
    expect(placeCount(s, installs.filter((p) => p.skill_id === 1))).toBe(3);
    expect(placeNames(s, installs.filter((p) => p.skill_id === 1))).toEqual(["Codex · 全局", "Cursor · 全局", "Codex、Cursor · web"]);
  });

  it("逐处列出时一处留第一条，保持原来的先后", () => {
    expect(eachPlace(s, installs.filter((p) => p.skill_id === 1))).toEqual([installs[0], installs[1], installs[2]]);
  });

  it("不同技能在同一处各算各的", () => {
    expect(placeCount(s, installs)).toBe(4);
    expect(eachPlace(s, installs).map((p) => p.skill_id)).toEqual([1, 1, 1, 2]);
  });

  it("只说地方、不分技能时直接给作用域", () => {
    expect(placeNames(s, [at(cursor, 1), at(codex, 1), at(claude, 1)])).toEqual(["Codex、Cursor · web", "Claude Code · web"]);
  });

  it("只问涉及几个地方时不分技能：几个技能装在同一处只算一个，名字也只写一次", () => {
    expect(scopeCount(s, installs)).toBe(3);
    expect(scopeCount(s, installs.map((p) => p.scope))).toBe(3);
    expect(placeNames(s, installs)).toEqual(["Codex · 全局", "Cursor · 全局", "Codex、Cursor · web"]);
  });

  it("没有共用目录时一条就是一处", () => {
    expect(placeCount(s, [placed(1, at(claude)), placed(1, at(claude, 1))])).toBe(2);
  });
});

describe("合成后的作用域", () => {
  it("项目里共用目录的几个落在代表的 Agent 上，全局和不共用的是它自己", () => {
    expect(mergedScope(s, at(cursor, 1))).toEqual(at(codex, 1));
    expect(mergedScope(s, at(cursor))).toEqual(at(cursor));
    expect(mergedScope(s, at(claude, 1))).toEqual(at(claude, 1));
    expect(mergedScopeKey(s, at(cursor, 1))).toBe(mergedScopeKey(s, at(codex, 1)));
    expect(sameMergedScope(s, at(cursor), at(codex))).toBe(false);
  });

  it("这台机器上只显示其中一个时，落在显示的那个上、名字也只写它", () => {
    const hidden = { ...s, agentsShown: { [codex]: false } } as unknown as Snapshot;
    expect(mergedScope(hidden, at(codex, 1))).toEqual(at(cursor, 1));
    expect(placeNames(hidden, [at(codex, 1)])).toEqual(["Cursor · web"]);
  });
});
