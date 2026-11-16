// 安装、卸载时怎样看提到关系：装好的那几处找不到哪些、卸载前多出哪些找不到的、能从库里补装哪几个。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, type MissingMention, type Scope } from "@bindings/internal/library";
import { installable, missingAt, missingNames, newlyMissing } from "./mentions";

const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id: id });
const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const missing = (skill: number, scope: Scope, name: string, mentioned = 0): MissingMention => ({
  skill_id: skill,
  scope,
  name,
  mentioned_skill_id: mentioned,
});

describe("missingAt", () => {
  it("只留装在这几处的技能找不到的", () => {
    const all = [missing(1, global(claude), "ship", 2), missing(1, project(claude, 3), "ship", 2), missing(4, global(claude), "pdf")];
    expect(missingAt(all, [{ skill_id: 1, scope: project(claude, 3) }])).toEqual([all[1]]);
    expect(missingAt(all, [{ skill_id: 1, scope: global(codex) }])).toEqual([]);
  });
});

describe("newlyMissing", () => {
  it("卸载前后比较，只留卸载之后才找不到的", () => {
    const before = [missing(1, global(claude), "pdf")];
    const after = [missing(1, global(claude), "pdf"), missing(1, global(claude), "ship", 2), missing(5, project(claude, 3), "ship", 2)];
    expect(newlyMissing(before, after)).toEqual(after.slice(1));
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
