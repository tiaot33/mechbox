// 安装、卸载与作用域页怎样看提到关系：装好的那几处找不到哪些(共用项目目录的几个 Agent 算一处)、列出时不重复、能从库里补装哪几个。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, type Agent, type MissingMention, type Scope } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { installable, mergeMissing, missingAt, missingNames } from "./mentions";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const amp = "amp" as AgentCode;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id: id });
const missing = (skill: number, scope: Scope, name: string, mentioned = 0): MissingMention => ({
  skill_id: skill,
  scope,
  name,
  mentioned_skill_id: mentioned,
});
// Codex 和 Amp 在项目里共用 .agents/skills,全局目录各是各的。
const agent = (code: AgentCode, projectDir: string) => ({ code, project_directories: [projectDir ]}) as unknown as Agent;
const s = { agents: [agent(claude, ".claude/skills"), agent(codex, ".agents/skills"), agent(amp, ".agents/skills")], agentsShown: {} } as unknown as Snapshot;

describe("missingAt", () => {
  it("只留装在这几处的技能找不到的", () => {
    const all = [missing(1, global(claude), "ship", 2), missing(1, project(claude, 3), "ship", 2), missing(4, global(claude), "pdf")];
    expect(missingAt(s, all, [{ skill_id: 1, scope: project(claude, 3) }])).toEqual([all[1]]);
    expect(missingAt(s, all, [{ skill_id: 1, scope: global(codex) }])).toEqual([]);
  });

  it("项目里共用目录的几个 Agent 是一处：按其中一个查，别的 Agent 找不到的也算", () => {
    const all = [missing(1, project(amp, 3), "ship", 2)];
    expect(missingAt(s, all, [{ skill_id: 1, scope: project(codex, 3) }])).toEqual(all);
  });
});

describe("mergeMissing", () => {
  it("共用目录的几个 Agent 里同一条只列一次，不同的名字照列", () => {
    const all = [missing(1, project(codex, 3), "ship"), missing(1, project(amp, 3), "ship"), missing(1, project(amp, 3), "pdf")];
    expect(mergeMissing(s, all)).toEqual([all[0], all[2]]);
  });
});

describe("installable", () => {
  it("库里有的才能补装，装到提到它的那一处，同一处只装一次", () => {
    const all = [
      missing(1, global(claude), "ship", 2),
      missing(4, global(claude), "ship", 2),
      missing(1, global(codex), "ship", 2),
      missing(1, global(claude), "local-tool"),
    ];
    expect(installable(all)).toEqual([
      { skill_id: 2, scope: global(claude) },
      { skill_id: 2, scope: global(codex) },
    ]);
  });
});

describe("missingNames", () => {
  it("按出现的先后，不重复", () => {
    expect(missingNames([missing(1, global(claude), "ship"), missing(4, global(claude), "pdf"), missing(1, global(codex), "ship")])).toEqual(["ship", "pdf"]);
  });
});
