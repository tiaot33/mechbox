// 拖放：拖到的地方已经有了就不接受放下。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, type Agent, type GroupInstall } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { alreadyInScope } from "./dnd";

describe("alreadyInScope", () => {
  // Codex、Amp 在项目里都只读 .agents/skills,合成一处;Amp 的代码排序更靠前，添加后成了代表。
  const agents = [
    { code: "amp", name: "Amp", global_directories: ["/home/.amp/skills"], project_directories: [".agents/skills"] },
    { code: AgentCode.AgentCodex, name: "Codex", global_directories: ["/home/.codex/skills"], project_directories: [".agents/skills"] },
  ] as Agent[];
  const s = {
    agents,
    agentsShown: {},
    groups: [{ id: 5, name: "前端" }],
    groupInstalls: [{ group_id: 5, scope: { kind: ScopeKind.ScopeProject, agent: AgentCode.AgentCodex, project_id: 7 } } as GroupInstall],
    skillScopes: [],
  } as unknown as Snapshot;
  it("组在合成一处里任何一个 Agent 上有组安装就算已在这里，代表换了也不再多记一条", () => {
    expect(alreadyInScope(s, { kind: "group", id: 5 }, "amp@7")).toBe(true);
    expect(alreadyInScope(s, { kind: "group", id: 5 }, "amp@0")).toBe(false);
  });
});
