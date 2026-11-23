// 设置页换全局目录：库里记着全局安装时先确认，确认框里数几处、怎样称呼位置。
import { describe, expect, it } from "vitest";
import { AgentCode, ScopeKind, SkillScopeStatus, type Agent, type Scope, type SkillScopeInspection } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { globalDirInUse, globalDirName, globalInstallsToConfirm, switchedToDistro } from "./global-dir-rules";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const cline = "cline" as AgentCode;
const cursor = "cursor" as AgentCode;
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

// Codex 和 Cline 的全局目录列表相同，合成一处;Cursor 多读一个目录，不和它们合成。
const agents = [
  { code: claude, name: "Claude Code", global_directories: ["~/.claude/skills"], project_directories: [".claude/skills"] },
  { code: codex, name: "Codex", global_directories: ["~/.agents/skills"], project_directories: [".agents/skills"] },
  { code: cline, name: "Cline", global_directories: ["~/.agents/skills"], project_directories: [".agents/skills"] },
  { code: cursor, name: "Cursor", global_directories: ["~/.agents/skills", "~/.cursor/skills"], project_directories: [".agents/skills"] },
] as Agent[];
const snapshot = (skillScopes: SkillScopeInspection[]) => ({ agents, agentsShown: {}, skillScopes }) as unknown as Snapshot;

describe("换全局目录之前要不要先确认", () => {
  it("库里记着全局安装时，换到 WSL 发行版、换回 Windows 本机、在两个发行版之间换都先确认，数出有几处", () => {
    const s = snapshot([place(1, global(claude)), place(2, global(claude)), place(1, global(codex))]);
    expect(globalInstallsToConfirm(s, "", "Ubuntu")).toBe(3);
    expect(globalInstallsToConfirm(s, "Ubuntu", "")).toBe(3);
    expect(globalInstallsToConfirm(s, "Ubuntu", "Debian")).toBe(3);
  });

  it("库里没有全局安装时直接换：空库、只装在项目里都不问", () => {
    expect(globalInstallsToConfirm(snapshot([]), "", "Ubuntu")).toBe(0);
    expect(globalInstallsToConfirm(snapshot([place(1, project(claude, 7)), place(2, project(codex, 7))]), "", "Ubuntu")).toBe(0);
  });

  it("实际用的位置没变时不算换，不问", () => {
    const s = snapshot([place(1, global(claude))]);
    expect(globalInstallsToConfirm(s, "", "")).toBe(0);
    expect(globalInstallsToConfirm(s, "Ubuntu", "Ubuntu")).toBe(0);
  });

  it("项目里的安装不算进全局安装的处数", () => {
    const s = snapshot([place(1, global(claude)), place(1, project(claude, 7)), place(2, project(codex, 7))]);
    expect(globalInstallsToConfirm(s, "", "Ubuntu")).toBe(1);
  });

  it("合成一处的几个 Agent 只算一处;只因共用目录才算已装的不是库里记着的安装，不算", () => {
    const shared = { shared_with: [codex, cline, cursor] };
    const s = snapshot([
      place(1, global(codex), { ...shared, folder: "/home/.agents/skills/s1" }),
      place(1, global(cline), { ...shared, folder: "/home/.agents/skills/s1" }),
      place(1, global(cursor), { ...shared, folder: "/home/.agents/skills/s1", shared_only: true }),
    ]);
    expect(globalInstallsToConfirm(s, "", "Ubuntu")).toBe(1);
  });

  it("只因共用目录才算已装的是唯一一条全局记录时，库里没有全局安装，直接换", () => {
    const s = snapshot([place(1, global(cursor), { shared_only: true })]);
    expect(globalInstallsToConfirm(s, "", "Ubuntu")).toBe(0);
  });

  it("随组安装到全局的技能也算", () => {
    const s = snapshot([place(1, global(claude), { direct: false, groups: [3] })]);
    expect(globalInstallsToConfirm(s, "", "Ubuntu")).toBe(1);
  });
});

describe("全局目录现在实际在哪", () => {
  it("没出错时就是设置里选的", () => {
    expect(globalDirInUse("", "")).toBe("");
    expect(globalDirInUse("Ubuntu", "")).toBe("Ubuntu");
  });

  it("选的发行版启动时没能打开时，实际用的是 Windows 本机", () => {
    expect(globalDirInUse("Ubuntu", "wsl distribution unavailable")).toBe("");
  });

  it("这时选回 Windows 本机，实际用的目录不变，不问;换到另一个发行版时问，原来位置是 Windows 本机", () => {
    const s = snapshot([place(1, global(claude))]);
    const before = globalDirInUse("Ubuntu", "wsl distribution unavailable");
    expect(globalInstallsToConfirm(s, before, "")).toBe(0);
    expect(globalInstallsToConfirm(s, before, "Debian")).toBe(1);
    expect(globalDirName(before)).toBe("Windows 本机");
  });
});

describe("换过去以后补弹添加 Agent", () => {
  it("换到另一个发行版时补弹一次候选池;换回 Windows 本机、没换时不弹", () => {
    expect(switchedToDistro("", "Ubuntu")).toBe(true);
    expect(switchedToDistro("Ubuntu", "Debian")).toBe(true);
    expect(switchedToDistro("Ubuntu", "")).toBe(false);
    expect(switchedToDistro("Ubuntu", "Ubuntu")).toBe(false);
  });
});

describe("全局目录所在位置的叫法", () => {
  it("和设置里的下拉选项一样:Windows 本机，或「WSL · 发行版」", () => {
    expect(globalDirName("")).toBe("Windows 本机");
    expect(globalDirName("Ubuntu")).toBe("WSL · Ubuntu");
  });
});
