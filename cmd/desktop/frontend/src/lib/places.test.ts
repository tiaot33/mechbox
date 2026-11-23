// 界面上的「一处」:同一层级目录列表完全相同的几个 Agent 合成一处(全局和项目都一样),写名字、数个数、逐处列出时都只算一次;
// 合成一处的几个 Agent 各自装在不同目录时按实际的文件夹分开。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, SkillScopeStatus, type Agent, type RegisteredProject, type Scope, type SkillScopeInspection } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { agentsIn, eachPlace, installAgentsIn, mergedScope, mergedScopeKey, placeCount, placeNames, sameMergedScope, scopeCount, sharedWithNote } from "./places";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const cursor = "cursor" as AgentCode;
const at = (agent: AgentCode, project_id = 0): Scope => (project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent });
// Codex 和 Cursor 在项目里都读 .agents/skills,全局目录各是各的。
const s = {
  agents: [
    { code: claude, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills"], detected: true },
    { code: codex, name: "Codex", global_directories: ["/home/.codex/skills"], project_directories: [".agents/skills"], detected: true },
    { code: cursor, name: "Cursor", global_directories: ["/home/.cursor/skills"], project_directories: [".agents/skills"], detected: true },
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

describe("全局也合成一处", () => {
  // Codex、Cline 全局都只读 ~/.agents/skills;Cursor 读 ~/.agents/skills 和 ~/.cursor/skills,列表不同。
  const cline = "cline" as AgentCode;
  const g = {
    ...s,
    agents: [
      { code: codex, name: "Codex", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills"] },
      { code: cline, name: "Cline", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills"] },
      { code: cursor, name: "Cursor", global_directories: ["/home/.agents/skills", "/home/.cursor/skills"], project_directories: [".agents/skills", ".cursor/skills"] },
    ] as Agent[],
  } as unknown as Snapshot;

  it("目录列表完全相同的合成一行，只共用某个目录的各占一行", () => {
    expect(placeNames(g, [at(codex), at(cline), at(cursor)])).toEqual(["Codex、Cline · 全局", "Cursor · 全局"]);
    expect(mergedScope(g, at(cline))).toEqual(at(codex));
  });

  it("卸载前只另说目录列表不同、只共用这个目录的 Agent", () => {
    expect(sharedWithNote(g, { scope: at(codex), shared_with: [cline, cursor] })).toEqual([cursor]);
  });
});

describe("合成一处的几个 Agent 各自装在不同目录", () => {
  // A、B 的项目目录列表都是 [.a/skills, .b/skills],foo(技能 1)分别装在 .a/skills 和 .b/skills:是两个文件夹。
  const a = "a" as AgentCode;
  const b = "b" as AgentCode;
  const inspection = (agent: AgentCode, dir: string, extra: Partial<SkillScopeInspection> = {}) =>
    ({ skill_id: 1, scope: at(agent, 1), directory: dir, folder: `/web/${dir}/foo`, direct: true, status: SkillScopeStatus.SkillScopeCurrent, ...extra }) as SkillScopeInspection;
  const split = {
    ...s,
    agents: [
      { code: a, name: "A", global_directories: ["/home/.a/skills"], project_directories: [".a/skills", ".b/skills"] },
      { code: b, name: "B", global_directories: ["/home/.b/skills"], project_directories: [".a/skills", ".b/skills"] },
    ] as Agent[],
    skillScopes: [inspection(a, ".a/skills"), inspection(b, ".b/skills")],
  } as unknown as Snapshot;

  it("按实际的文件夹分成两处，各写读那个文件夹的 Agent", () => {
    expect(placeCount(split, split.skillScopes)).toBe(2);
    expect(placeNames(split, split.skillScopes)).toEqual(["A · web", "B · web"]);
  });

  it("没给文件夹的一条按快照里这个技能在那里的文件夹算", () => {
    expect(placeCount(split, [placed(1, at(a, 1)), placed(1, at(b, 1))])).toBe(2);
  });

  it("读同一个文件夹的照旧是一处，名字写合成的那几个", () => {
    const same = { ...split, skillScopes: [inspection(a, ".a/skills", { shared_with: [b] }), inspection(b, ".a/skills", { shared_with: [a], shared_only: true })] } as unknown as Snapshot;
    expect(placeCount(same, same.skillScopes)).toBe(1);
    expect(placeNames(same, same.skillScopes)).toEqual(["A、B · web"]);
  });
});

describe("右栏列哪些 Agent", () => {
  // Codex 全局只读 ~/.agents/skills,Cursor 还读 ~/.cursor/skills:目录列表不同，各占一行。
  const base = {
    ...s,
    agents: [
      { code: codex, name: "Codex", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills"], detected: true },
      { code: cursor, name: "Cursor", global_directories: ["/home/.agents/skills", "/home/.cursor/skills"], project_directories: [".agents/skills"], detected: true },
    ] as Agent[],
    agentsShown: { [cursor]: false },
  } as unknown as Snapshot;
  const installed = (agent: AgentCode, extra: Partial<SkillScopeInspection> = {}) =>
    ({ skill_id: 1, scope: at(agent), directory: "/home/.agents/skills", folder: "/home/.agents/skills/foo", direct: true, status: SkillScopeStatus.SkillScopeCurrent, ...extra }) as SkillScopeInspection;

  it("只因共用目录被动算出已装、又被关掉显示的 Agent 不强行列出", () => {
    const passive = { ...base, skillScopes: [installed(codex, { shared_with: [cursor] }), installed(cursor, { shared_with: [codex], shared_only: true })] } as unknown as Snapshot;
    expect(agentsIn(passive, 0).open.map((x) => x.code)).toEqual([codex]);
  });

  it("关掉显示、但自己装了技能的照常列出", () => {
    const own = { ...base, skillScopes: [installed(cursor)] } as unknown as Snapshot;
    expect(agentsIn(own, 0).open.map((x) => x.code)).toEqual([codex, cursor]);
  });

  it("一组都关掉显示、但自己有记录时留一个代表", () => {
    const both = {
      ...base,
      agents: base.agents.map((x) => ({ ...x, global_directories: ["/home/.agents/skills"] })),
      agentsShown: { [codex]: false, [cursor]: false },
      skillScopes: [installed(cursor, { shared_with: [codex] }), installed(codex, { shared_with: [cursor], shared_only: true })],
    } as unknown as Snapshot;
    expect(agentsIn(both, 0).open.map((x) => x.code)).toEqual([codex]);
    expect(placeNames(both, [at(cursor)])).toEqual(["Codex · 全局"]);
  });
});

describe("安装到…列哪些 Agent", () => {
  const installed = (agent: AgentCode, project_id = 0) =>
    ({ skill_id: 1, scope: at(agent, project_id), folder: "f", direct: true, status: SkillScopeStatus.SkillScopeCurrent }) as SkillScopeInspection;

  it("在这里装有技能的在前，其余勾选显示的接在后面;读同一个目录、合成一处的不重复列", () => {
    const used = { ...s, skillScopes: [installed(cursor, 1)] } as unknown as Snapshot;
    const r = installAgentsIn(used, 1);
    expect(r.used.map((a) => a.code)).toEqual([codex]);
    expect(r.rest.map((a) => a.code)).toEqual([claude]);
    expect(installAgentsIn(used, 0)).toEqual({ used: [], rest: s.agents });
  });

  it("没勾选显示的不列在其余里", () => {
    const hidden = { ...s, agentsShown: { [claude]: false }, skillScopes: [] } as unknown as Snapshot;
    expect(installAgentsIn(hidden, 0).rest.map((a) => a.code)).toEqual([codex, cursor]);
  });
});
