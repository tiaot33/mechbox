// 安装状态：一处要不要重新安装、算不算要修的问题，技能库页一行的「装在哪」和装出去几处的提醒。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ErrorCode,
  ScopeKind,
  SkillScopeStatus,
  SourceType,
  type Agent,
  type Group,
  type GroupInstall,
  type RegisteredProject,
  type Scope,
  type Skill,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import type { Snapshot } from "./library";
import {
  editedHere,
  errorOccupier,
  folderPresent,
  groupInstallsIn,
  groupStateIn,
  installedSummary,
  needsReinstall,
  needsRepair,
  occupierOf,
  reinstallAlerts,
  reinstallLevel,
  placesIn,
  skillCount,
} from "./installs";
import { scopeKey } from "./scope";

const snapshot = (s: Partial<Snapshot>) =>
  ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;
const skill = (id: number, name: string, label: string) => ({ id, name, source: { type: SourceType.SourceGitHub, label } }) as Skill;
const where = (agent: string, project_id?: number) =>
  project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
const installed = (skill_id: number, agent: string, project_id?: number) => ({ skill_id, scope: where(agent, project_id) }) as SkillScopeInspection;

describe("placesIn", () => {
  it("合成一处的几个 Agent 各自装在不同目录时，每一处都算(右栏、作用域页的提醒按它);共用的同一个文件夹只算一处", () => {
    const agents = [
      { code: AgentCode.AgentClaudeCode, name: "Claude Code", global_directories: ["/home/.dir1", "/home/.dir2"], project_directories: [".claude/skills"] },
      { code: AgentCode.AgentCodex, name: "Codex", global_directories: ["/home/.dir1", "/home/.dir2"], project_directories: [".codex/skills"] },
    ] as Agent[];
    const p = (skill_id: number, agent: string, folder: string, status = SkillScopeStatus.SkillScopeCurrent) =>
      ({ skill_id, scope: where(agent, undefined), folder, direct: true, status }) as SkillScopeInspection;
    const mine = p(1, AgentCode.AgentClaudeCode, "/home/.dir1/s1");
    const theirs = p(1, AgentCode.AgentCodex, "/home/.dir2/s1", SkillScopeStatus.SkillScopeMissing);
    const shared = p(2, AgentCode.AgentClaudeCode, "/home/.dir1/s2");
    const s = snapshot({ agents, skillScopes: [mine, shared, theirs, p(2, AgentCode.AgentCodex, "/home/.dir1/s2")] });
    const key = scopeKey(where(AgentCode.AgentClaudeCode) as Scope);
    expect(placesIn(s, key)).toEqual([mine, shared, theirs]);
    expect(reinstallLevel(placesIn(s, key))).toBe("critical");
    expect(skillCount(placesIn(s, key))).toBe(2);
  });
});

describe("folderPresent", () => {
  it("文件夹不在了、项目不在这台机器上的不算在磁盘上，其余都算", () => {
    const present = (status: SkillScopeStatus) => folderPresent({ status });
    expect(present(SkillScopeStatus.SkillScopeMissing)).toBe(false);
    expect(present(SkillScopeStatus.SkillScopeRootMissing)).toBe(false);
    expect(present(SkillScopeStatus.SkillScopeCurrent)).toBe(true);
    expect(present(SkillScopeStatus.SkillScopeStale)).toBe(true);
    expect(present(SkillScopeStatus.SkillScopeModified)).toBe(true);
  });
});

describe("项目不在这台机器上", () => {
  const scope = { kind: ScopeKind.ScopeProject, agent: AgentCode.AgentClaudeCode, project_id: 2 } as Scope;
  const place = (skill_id: number, status: SkillScopeStatus) => ({ skill_id, scope, folder: `f${skill_id}`, direct: true, status }) as SkillScopeInspection;

  it("装在那里的技能不算要重新安装，不带提醒;提醒只在项目上", () => {
    const off = place(1, SkillScopeStatus.SkillScopeRootMissing);
    expect(needsReinstall(off)).toBe(false);
    expect(reinstallLevel([off])).toBeUndefined();
    const s = snapshot({
      agents: [{ code: AgentCode.AgentClaudeCode, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills" ]}] as Agent[],
      projects: [{ id: 2, name: "old", missing: true }] as RegisteredProject[],
      skills: [{ id: 1, name: "s1" }] as Skill[],
      skillScopes: [off],
    });
    expect(installedSummary(s, 1).bad).toBe(false);
  });

  it("技能库页的一行：同一个原因的几处合成一条，说明里逐行写出;在 app 外更新到作者新版的归为更新;项目不在这台机器上的不出现", () => {
    const at = (agent: AgentCode, project_id = 0) =>
      (project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent }) as Scope;
    const p = (scope: Scope, status: SkillScopeStatus, source_latest = false) =>
      ({ skill_id: 1, scope, folder: `${scope.agent}${scope.project_id ?? 0}`, direct: true, status, source_latest }) as SkillScopeInspection;
    const s = snapshot({
      agents: [
        { code: AgentCode.AgentClaudeCode, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills" ]},
        { code: AgentCode.AgentCodex, name: "Codex", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills" ]},
      ] as Agent[],
      projects: [{ id: 2, name: "old", missing: true }, { id: 3, name: "web" }] as RegisteredProject[],
      skills: [{ id: 1, name: "s1" }] as Skill[],
      skillScopes: [
        p(at(AgentCode.AgentClaudeCode), SkillScopeStatus.SkillScopeMissing),
        p(at(AgentCode.AgentCodex), SkillScopeStatus.SkillScopeMissing),
        p(at(AgentCode.AgentClaudeCode, 3), SkillScopeStatus.SkillScopeModified, true),
        p(at(AgentCode.AgentClaudeCode, 2), SkillScopeStatus.SkillScopeRootMissing),
        p(at(AgentCode.AgentCodex, 3), SkillScopeStatus.SkillScopeCurrent),
      ],
    });
    const alerts = reinstallAlerts(s, 1);
    expect(alerts.map((a) => a.tone)).toEqual(["critical", "update"]);
    // 要重新安装的先写一句，再逐行写地方;作者新版那条是更新，只列地方。
    expect(alerts[0].tip.split("\n")).toEqual(["要重新安装的地方：", "Claude Code · 全局", "Codex · 全局"]);
    expect(alerts[1].tip).toBe("Claude Code · web");
  });

  it("同一个技能在别处文件夹不在了，照样是严重", () => {
    expect(reinstallLevel([place(1, SkillScopeStatus.SkillScopeRootMissing), place(1, SkillScopeStatus.SkillScopeMissing)])).toBe("critical");
  });
});

describe("被手动改过是用户自己的修改", () => {
  const scope = { kind: ScopeKind.ScopeGlobal, agent: AgentCode.AgentClaudeCode } as Scope;
  const place = (status: SkillScopeStatus, source_latest = false) =>
    ({ skill_id: 1, scope, folder: "f1", direct: true, status, source_latest }) as SkillScopeInspection;
  const s = snapshot({
    agents: [{ code: AgentCode.AgentClaudeCode, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills" ]}] as Agent[],
    skills: [{ id: 1, name: "s1" }] as Skill[],
    skillScopes: [place(SkillScopeStatus.SkillScopeModified)],
  });

  it("不算要修的问题：不计入需重新安装，提醒是提示一级", () => {
    const edited = place(SkillScopeStatus.SkillScopeModified);
    expect(editedHere(edited)).toBe(true);
    expect(needsRepair(edited)).toBe(false);
    // 重新安装时仍能勾上覆盖它。
    expect(needsReinstall(edited)).toBe(true);
    expect(reinstallLevel([edited])).toBe("info");
    expect(installedSummary(s, 1).bad).toBe(false);
  });

  it("文件夹不在了、版本落后才要重新安装;在 app 外更新到作者新版的不算被手动改过", () => {
    expect(needsRepair(place(SkillScopeStatus.SkillScopeMissing))).toBe(true);
    expect(needsRepair(place(SkillScopeStatus.SkillScopeStale))).toBe(true);
    expect(editedHere(place(SkillScopeStatus.SkillScopeModified, true))).toBe(false);
    expect(reinstallLevel([place(SkillScopeStatus.SkillScopeModified), place(SkillScopeStatus.SkillScopeMissing)])).toBe("critical");
  });

  it("技能库页的一行：说明里不写「要重新安装」，写可以用这里的修改更新", () => {
    const [alert] = reinstallAlerts(s, 1);
    expect(alert.tone).toBe("info");
    expect(alert.tip.split("\n")).toEqual(["在这些地方被手动改过，可以用这里的修改更新技能库：", "Claude Code · 全局"]);
  });
});

describe("installedSummary", () => {
  it("全局写出装在哪几个 Agent,装在列出的每一个 Agent 时也照样写出名字", () => {
    const agents = ["claude-code", "codex", "cursor"].map((code) => ({ code, name: code.toUpperCase(), detected: true }) as Agent);
    const s = snapshot({
      agents,
      projects: [{ id: 2, name: "web" }] as RegisteredProject[],
      skills: [skill(1, "s1", "o/r")],
      skillScopes: [installed(1, "claude-code"), installed(1, "codex"), installed(1, "cursor"), installed(1, "codex", 2)],
    });
    expect(installedSummary(s, 1).text).toBe("CLAUDE-CODE、CODEX、CURSOR 全局、web");
  });
});

describe("装不上时占着文件夹的同名技能", () => {
  const s = snapshot({ skills: [skill(1, "tdd", "a/skills"), skill(2, "tdd", "b/skills")] });

  it("安装被占用时，占着的是库里同名的另一个技能才能点开它", () => {
    expect(occupierOf(s, 1, 2)).toBe(2);
    // 占着的是这个技能自己的另一个作用域、不在快照里、没有占用者：没有可打开的。
    expect(occupierOf(s, 1, 1)).toBeUndefined();
    expect(occupierOf(s, 1, 9)).toBeUndefined();
    expect(occupierOf(s, 1, undefined)).toBeUndefined();
  });

  it("安装失败的错误里，被占用(FailedPrecondition)时才有占着的技能", () => {
    expect(errorOccupier(s, 1, { code: ErrorCode.FailedPrecondition, details: { skill_id: 2 } })).toBe(2);
    expect(errorOccupier(s, 1, { code: ErrorCode.NotFound, details: { skill_id: 2 } })).toBeUndefined();
    expect(errorOccupier(s, 1, undefined)).toBeUndefined();
  });
});

describe("installedSummary 的装在哪", () => {
  // Cline、Codex 全局都只读 ~/.agents/skills,合成一处;Cursor 被关掉显示，只因共用目录被动算出已装。
  const agents = [
    { code: "cline", name: "Cline", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills"] },
    { code: AgentCode.AgentCodex, name: "Codex", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills"] },
    { code: "cursor", name: "Cursor", global_directories: ["/home/.agents/skills", "/home/.cursor/skills"], project_directories: [".agents/skills"] },
  ] as Agent[];
  const at = (agent: string, extra: Partial<SkillScopeInspection> = {}) =>
    ({ skill_id: 1, scope: where(agent), folder: "/home/.agents/skills/x", direct: true, status: SkillScopeStatus.SkillScopeCurrent, ...extra }) as SkillScopeInspection;
  it("合成一处的几个 Agent 只写一次，被关掉显示、只是被动算出已装的不写", () => {
    const s = snapshot({
      agents,
      agentsShown: { cursor: false },
      skillScopes: [at("cline"), at(AgentCode.AgentCodex), at("cursor", { shared_only: true })],
    } as Partial<Snapshot>);
    expect(installedSummary(s, 1).text).toBe("Cline、Codex 全局");
  });
});

describe("groupStateIn", () => {
  // Codex、Amp 在项目里都只读 .agents/skills,合成一处;Amp 的代码排序更靠前，添加后成了代表。
  const agents = [
    { code: "amp", name: "Amp", global_directories: ["/home/.amp/skills"], project_directories: [".agents/skills"] },
    { code: AgentCode.AgentCodex, name: "Codex", global_directories: ["/home/.codex/skills"], project_directories: [".agents/skills"] },
  ] as Agent[];
  const group = { id: 5, name: "前端" } as Group;
  const s = snapshot({ agents, agentsShown: {}, groupInstalls: [{ group_id: 5, scope: where(AgentCode.AgentCodex, 7) } as GroupInstall] });

  it("合成一处里任何一个 Agent 有组安装就是开，代表换了也不变", () => {
    expect(groupStateIn(s, group, "amp@7")).toBe("on");
    expect(groupStateIn(s, group, `${AgentCode.AgentCodex}@7`)).toBe("on");
  });

  it("别的层级、不合成的 Agent 的组安装不算", () => {
    expect(groupStateIn(s, group, "amp@0")).toBe("off");
  });

  it("合成一处里几个 Agent 各有一条组安装时都列出来，关掉时一起撤", () => {
    const both = snapshot({ ...s, groupInstalls: [...s.groupInstalls, { group_id: 5, scope: where("amp", 7) } as GroupInstall] });
    expect(groupInstallsIn(both, group, "amp@7").map((x) => x.agent).sort()).toEqual(["amp", AgentCode.AgentCodex]);
  });
});
