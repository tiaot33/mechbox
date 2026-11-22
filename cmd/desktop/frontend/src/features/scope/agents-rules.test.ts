// Agent:改目录时哪些已装的技能、组安装受影响，移除 Agent 时哪些文件夹归它，候选池的顺序与「已有 Agent 读这个目录」。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ScopeKind,
  SkillScopeStatus,
  type Agent,
  type AgentInput,
  type GroupInstall,
  type Scope,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { agentDelete, agentEdit, alreadyRead, poolMatches, sortPool } from "./agents-rules";

const mine = "my-agent" as AgentCode;
const claude = AgentCode.AgentClaudeCode;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const place = (skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}): SkillScopeInspection => ({
  directory: "",
  skill_id,
  scope,
  folder: `/${scope.project_id ?? "home"}/${scope.agent}/s${skill_id}`,
  direct: true,
  status: SkillScopeStatus.SkillScopeCurrent,
  ...extra,
});

const agent = { code: mine, name: "Mine", global_directories: ["/home/.mine/skills"], project_directories: [".mine/skills" ]} as Agent;
const input = (extra: Partial<AgentInput>) => ({ name: "Mine", global_directories: ["/home/.mine/skills"], project_directories: [".mine/skills"], ...extra }) as AgentInput;

const snapshot = (skillScopes: SkillScopeInspection[], groupInstalls: GroupInstall[] = []) => ({ skillScopes, groupInstalls }) as unknown as Snapshot;

describe("改 Agent 的第 1 个目录", () => {
  const s = snapshot(
    [place(1, global(mine), { shared_with: [claude] }), place(2, global(mine), { linked_by: [global(claude)] }), place(3, project(mine, 7)), place(4, global(claude))],
    [{ group_id: 5, scope: project(mine, 7) } as GroupInstall],
  );

  it("写法不同的同一个目录不算改了，不问", () => {
    expect(agentEdit(s, agent, input({ global_directories: ["/home/.mine/skills/"], project_directories: ["./.mine/skills" ]}))).toMatchObject({ affected: [], groupInstalls: 0, asks: false });
  });

  it("只算改了的那一层：全局改了算全局装的;还有别的 Agent 在读的另说，别处链接指着要搬走的先换成独立的一份", () => {
    const r = agentEdit(s, agent, input({ global_directories: ["/home/.other" ]}));
    expect(r.affected.map((p) => p.skill_id)).toEqual([1, 2]);
    expect(r.groupInstalls).toBe(0);
    expect(r.asks).toBe(true);
    expect(r.shared.map((p) => p.skill_id)).toEqual([1]);
    expect(r.detached.map((d) => d.place.skill_id)).toEqual([2]);
  });

  it("项目目录改了算各项目里装的和组安装;只有组安装时也问", () => {
    const r = agentEdit(s, agent, input({ project_directories: [".other" ]}));
    expect(r.affected.map((p) => p.skill_id)).toEqual([3]);
    expect(r.groupInstalls).toBe(1);
    expect(agentEdit(snapshot([], s.groupInstalls), agent, input({ project_directories: [".other" ]})).asks).toBe(true);
  });
});

describe("移除 Agent", () => {
  it("只有它在读的文件夹归它;还有别的 Agent 在读的保留，别的 Agent 的不算", () => {
    const r = agentDelete(snapshot([place(1, global(mine), { shared_with: [claude] }), place(2, global(mine)), place(3, global(claude))]), mine);
    expect(r.kept.map((p) => p.skill_id)).toEqual([1]);
    expect(r.own.map((p) => p.skill_id)).toEqual([2]);
  });

  it("只因共用目录才算已装的不删，算在保留的里", () => {
    const shared = place(1, global(mine), { shared_with: [claude], shared_only: true });
    expect(agentDelete(snapshot([shared]), mine)).toEqual({ kept: [shared], own: [] });
  });
});

describe("改 Agent 的附加目录", () => {
  // Cursor 全局读 ~/.agents/skills 和 ~/.cursor/skills:技能 1 装在第 1 个目录，技能 2 原地入库在附加目录;项目里有组安装。
  const cursor = "cursor" as AgentCode;
  const agent = {
    code: cursor,
    name: "Cursor",
    global_directories: ["/home/.agents/skills", "/home/.cursor/skills"],
    project_directories: [".agents/skills", ".cursor/skills"],
  } as Agent;
  const s = snapshot(
    [
      place(1, global(cursor), { directory: "/home/.agents/skills" }),
      place(2, global(cursor), { directory: "/home/.cursor/skills" }),
      place(3, project(cursor, 7), { directory: ".cursor/skills" }),
    ],
    [{ group_id: 5, scope: project(cursor, 7) } as GroupInstall],
  );
  const edit = (global_directories: string[], project_directories: string[]) =>
    agentEdit(s, agent, { name: "Cursor", global_directories, project_directories } as AgentInput);

  it("只有所在目录被去掉的安装要迁移：去掉附加目录只算装在那里的", () => {
    const r = edit(["/home/.agents/skills"], [".agents/skills", ".cursor/skills"]);
    expect(r.affected.map((p) => p.skill_id)).toEqual([2]);
    expect(r.groupInstalls).toBe(0);
  });

  it("改了第 1 个目录、装在附加目录里的不动;组安装按原来的第 1 个目录是否被去掉算", () => {
    const r = edit(["/home/.cursor/skills"], [".cursor/skills"]);
    expect(r.affected.map((p) => p.skill_id)).toEqual([1]);
    expect(r.groupInstalls).toBe(1);
  });

  it("只调换目录的顺序不算去掉，不问", () => {
    expect(edit(["/home/.cursor/skills", "/home/.agents/skills"], [".agents/skills", ".cursor/skills"]).asks).toBe(false);
  });
});

describe("候选池", () => {
  const entry = (code: string, name: string, global: string[], project: string[], detected = false) =>
    ({ code: code as AgentCode, name, detected, global_directories: global, project_directories: project }) as Agent;
  const cursor = entry("cursor", "Cursor", ["/home/.agents/skills", "/home/.cursor/skills"], [".agents/skills", ".cursor/skills"]);
  const cline = entry("cline", "Cline", ["/home/.agents/skills"], [".agents/skills"], true);
  const amp = entry("amp", "Amp", ["/home/.config/agents/skills"], [".agents/skills"]);
  const pool = [cursor, cline, amp];

  it("检测到的排在前面，其余按名称", () => {
    expect(sortPool(pool).map((a) => a.name)).toEqual(["Cline", "Amp", "Cursor"]);
  });

  it("按名称、目录搜索", () => {
    expect(pool.filter((a) => poolMatches(a, "CUR")).map((a) => a.name)).toEqual(["Cursor"]);
    expect(pool.filter((a) => poolMatches(a, ".config")).map((a) => a.name)).toEqual(["Amp"]);
    expect(pool.filter((a) => poolMatches(a, " ")).length).toBe(3);
  });

  it("独有的目录已经有已添加的 Agent 在读时标出;许多 Agent 都读的 .agents/skills 不比", () => {
    // 升级前手填过一个读 .cursor/skills 的 Agent;Codex 只读 .agents/skills。
    const old = entry("custom-1", "My Cursor", ["/home/.cursor/skills/"], ["./.cursor/skills"]);
    const codex = entry("codex", "Codex", ["/home/.agents/skills"], [".agents/skills"]);
    expect(alreadyRead(pool, [old, codex], cursor)).toEqual([
      { dir: "/home/.cursor/skills", project: false, by: [old] },
      { dir: ".cursor/skills", project: true, by: [old] },
    ]);
    expect(alreadyRead(pool, [old, codex], cline)).toEqual([]);
  });
});
