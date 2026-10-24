// 守住 ADR 0010 的默认勾选与写之前的说明：用户没有点头，Agent 用的技能就不变。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  PlannedChange,
  ScopeKind,
  SkillScopeStatus,
  type PlannedSkill,
  type Scope,
  type Skill,
  type SkillScopeInspection,
  type UpdatePlan,
} from "@bindings/internal/library";
import {
  defaultInstallOn,
  defaultSkills,
  detachedBy,
  linkWrites,
  overwriteKey,
  planReinstall,
  refreshRequests,
  reinstallTargets,
  updateInstalls,
  type ReinstallChoices,
} from "./consent";
import { folderKey } from "./model";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });

function place(skill_id: number, scope: Scope, status: SkillScopeStatus, extra: Partial<SkillScopeInspection> = {}): SkillScopeInspection {
  const where = scope.project_id ? `/p${scope.project_id}/.${scope.agent}` : `/home/.${scope.agent}`;
  return { skill_id, scope, folder: `${where}/skills/s${skill_id}`, direct: true, status, ...extra };
}

function planned(id: number, change: PlannedChange, extra: Partial<PlannedSkill> = {}): PlannedSkill {
  return { skill: { id, name: `s${id}` } as Skill, change, ...extra };
}

function plan(skills: PlannedSkill[], installs: SkillScopeInspection[] = []): UpdatePlan {
  return { id: "plan-1", collection_id: 1, source: {} as UpdatePlan["source"], skills, installs };
}

const nothingChosen = (): ReinstallChoices => ({ overwrite: new Set(), missing: new Map() });

describe("更新计划默认勾哪些技能", () => {
  it("有变化、没跳过的默认勾上", () => {
    const p = plan([planned(1, PlannedChange.PlannedChanged), planned(2, PlannedChange.PlannedUnchanged)]);
    expect([...defaultSkills(p)]).toEqual([1]);
  });

  it("用户改过的技能有变化时不默认勾：勾上才算同意换掉修改", () => {
    const p = plan([planned(1, PlannedChange.PlannedChanged, { edited: true }), planned(2, PlannedChange.PlannedChanged)]);
    expect([...defaultSkills(p)]).toEqual([2]);
  });

  it("跳过的改动不默认勾", () => {
    const p = plan([planned(1, PlannedChange.PlannedChanged, { skipped: true })]);
    expect(defaultSkills(p).size).toBe(0);
  });

  it("来源里已删除的、推测出的改名都不默认勾", () => {
    const p = plan([planned(1, PlannedChange.PlannedDeleted), planned(2, PlannedChange.PlannedDeleted, { renamed_to: "s2-kit" })]);
    expect(defaultSkills(p).size).toBe(0);
  });

  it("从技能发起时只勾这一个，即使集合里还有别的变化", () => {
    const p = plan([planned(1, PlannedChange.PlannedChanged), planned(2, PlannedChange.PlannedChanged)]);
    expect([...defaultSkills(p, [2])]).toEqual([2]);
  });

  it("从技能发起时，这个技能是用户改过的、已删除的或推测改名的，也不默认勾", () => {
    const p = plan([
      planned(1, PlannedChange.PlannedChanged, { edited: true }),
      planned(2, PlannedChange.PlannedDeleted),
      planned(3, PlannedChange.PlannedDeleted, { renamed_to: "s3-kit" }),
    ]);
    expect(defaultSkills(p, [1, 2, 3]).size).toBe(0);
  });
});

describe("装出去的地方默认勾哪些", () => {
  it("和库里一致或落后的地方默认勾", () => {
    expect(defaultInstallOn(place(1, global(claude), SkillScopeStatus.SkillScopeCurrent))).toBe(true);
    expect(defaultInstallOn(place(1, global(claude), SkillScopeStatus.SkillScopeStale))).toBe(true);
  });

  it("用户改过的、文件夹不在了的、项目不在这台机器上的默认不勾", () => {
    expect(defaultInstallOn(place(1, global(claude), SkillScopeStatus.SkillScopeModified))).toBe(false);
    expect(defaultInstallOn(place(1, global(claude), SkillScopeStatus.SkillScopeMissing))).toBe(false);
    expect(defaultInstallOn(place(1, project(claude, 7), SkillScopeStatus.SkillScopeRootMissing))).toBe(false);
  });
});

describe("一次更新要换的地方", () => {
  const installs = [
    place(1, global(claude), SkillScopeStatus.SkillScopeStale),
    place(1, global(codex), SkillScopeStatus.SkillScopeModified),
    place(1, project(claude, 7), SkillScopeStatus.SkillScopeMissing),
    place(1, project(codex, 8), SkillScopeStatus.SkillScopeRootMissing),
    place(2, global(claude), SkillScopeStatus.SkillScopeStale),
  ];
  const p = plan([planned(1, PlannedChange.PlannedChanged), planned(2, PlannedChange.PlannedChanged)], installs);

  it("按默认勾选：只换没改过、还在的地方，不覆盖、不装回去;没勾的技能装出去的地方不动", () => {
    const u = updateInstalls(p, new Set([1]), defaultInstallOn);
    expect(u.requests).toEqual([{ skill_id: 1, scope: global(claude), overwrite: false }]);
    expect(u.keptEdited).toBe(1);
    expect(u.keptMissing).toBe(1);
  });

  it("用户勾上改过的、文件夹不在了的地方才覆盖、才装回去", () => {
    const u = updateInstalls(p, new Set([1]), () => true);
    expect(u.requests).toEqual([
      { skill_id: 1, scope: global(claude), overwrite: false },
      { skill_id: 1, scope: global(codex), overwrite: true },
      { skill_id: 1, scope: project(claude, 7), overwrite: true },
    ]);
    expect(u.keptEdited).toBe(0);
    expect(u.keptMissing).toBe(0);
  });

  it("项目不在这台机器上的地方勾上了也不换", () => {
    const u = updateInstalls(p, new Set([1]), () => true);
    expect(u.requests.some((r) => r.scope.project_id === 8)).toBe(false);
  });
});

describe("本地目录来源的重新读取(目录内容没变)", () => {
  it("只补换落后的地方;用户改过的、文件夹不在了的不动，项目不在这台机器上的不算", () => {
    const r = refreshRequests([
      place(1, global(claude), SkillScopeStatus.SkillScopeStale),
      place(1, global(codex), SkillScopeStatus.SkillScopeModified),
      place(1, project(claude, 7), SkillScopeStatus.SkillScopeMissing),
      place(1, project(codex, 8), SkillScopeStatus.SkillScopeRootMissing),
      place(1, project(claude, 9), SkillScopeStatus.SkillScopeCurrent),
    ]);
    expect(r.requests).toEqual([{ skill_id: 1, scope: global(claude), overwrite: false }]);
    expect(r.keptEdited).toBe(1);
    expect(r.keptMissing).toBe(1);
  });
});

describe("重新安装", () => {
  const stale = place(1, global(claude), SkillScopeStatus.SkillScopeStale);
  const modified = place(1, global(codex), SkillScopeStatus.SkillScopeModified);
  const missing = place(2, global(claude), SkillScopeStatus.SkillScopeMissing);
  const current = place(3, global(claude), SkillScopeStatus.SkillScopeCurrent);
  const inspected = [stale, modified, missing, current];

  it("只换不是当前版本的地方;被改过的、文件夹不在了的要问用户", () => {
    const r = reinstallTargets(inspected, false);
    expect(r.targets).toEqual([stale, modified, missing]);
    expect(r.modified).toEqual([modified]);
    expect(r.missing).toEqual([missing]);
  });

  it("什么都不选时：被改过的不覆盖，文件夹不在了的不动", () => {
    const { targets } = reinstallTargets(inspected, false);
    const r = planReinstall(targets, false, nothingChosen());
    expect(r.requests).toEqual([{ skill_id: 1, scope: global(claude), overwrite: false }]);
    expect(r.uninstalls).toEqual([]);
    expect(r.keptModified).toBe(1);
    expect(r.keptMissing).toBe(1);
  });

  it("勾上的被改过的地方才覆盖，选装回去的才装回去", () => {
    const { targets } = reinstallTargets(inspected, false);
    const chosen: ReinstallChoices = { overwrite: new Set([overwriteKey(modified)]), missing: new Map([[folderKey(missing), "putBack"]]) };
    const r = planReinstall(targets, false, chosen);
    expect(r.requests).toEqual([
      { skill_id: 1, scope: global(claude), overwrite: false },
      { skill_id: 1, scope: global(codex), overwrite: true },
      { skill_id: 2, scope: global(claude), overwrite: true },
    ]);
    expect(r.keptModified).toBe(0);
    expect(r.keptMissing).toBe(0);
  });

  it("文件夹不在了的选卸载：卸载这一处，不装回去", () => {
    const { targets } = reinstallTargets(inspected, false);
    const r = planReinstall(targets, false, { overwrite: new Set(), missing: new Map([[folderKey(missing), "uninstall"]]) });
    expect(r.uninstalls).toEqual([missing]);
    expect(r.requests.some((x) => x.skill_id === 2)).toBe(false);
  });

  it("只点了一处时，文件夹不在了也直接装回去，不再问", () => {
    const r = reinstallTargets([missing], true);
    expect(r.missing).toEqual([]);
    expect(planReinstall(r.targets, true, nothingChosen()).requests).toEqual([{ skill_id: 2, scope: global(claude), overwrite: true }]);
  });

  it("项目里共用一个文件夹的几处按文件夹勾选、只卸载一次", () => {
    const a = place(4, project(claude, 7), SkillScopeStatus.SkillScopeMissing, { folder: "/p7/.agents/skills/s4" });
    const b = place(4, project(codex, 7), SkillScopeStatus.SkillScopeMissing, { folder: "/p7/.agents/skills/s4" });
    const r = planReinstall([a, b], false, { overwrite: new Set(), missing: new Map([[folderKey(a), "uninstall"]]) });
    expect(r.uninstalls).toHaveLength(1);
    const m1 = place(5, project(claude, 7), SkillScopeStatus.SkillScopeModified, { folder: "/p7/.agents/skills/s5" });
    const m2 = place(5, project(codex, 7), SkillScopeStatus.SkillScopeModified, { folder: "/p7/.agents/skills/s5" });
    expect(overwriteKey(m1)).toBe(overwriteKey(m2));
    expect(planReinstall([m1, m2], false, nothingChosen()).keptModified).toBe(1);
  });
});

describe("写之前说明符号链接", () => {
  it("要写的地方本身是符号链接时说出来", () => {
    const linked = place(1, global(claude), SkillScopeStatus.SkillScopeStale, { link_target: "/home/.agents/skills/s1" });
    const plain = place(1, global(codex), SkillScopeStatus.SkillScopeStale);
    expect(linkWrites([linked, plain], () => true).links).toEqual([linked]);
    expect(linkWrites([linked, plain], (p) => p === plain).links).toEqual([]);
  });

  it("别处的链接指着要覆盖或删除的文件夹、而那一处没一起动时，说出来", () => {
    const target = place(1, global(claude), SkillScopeStatus.SkillScopeStale, { linked_by: [global(codex)] });
    expect(detachedBy([target])).toEqual([{ place: target, by: [global(codex)] }]);
    const linker = place(1, global(codex), SkillScopeStatus.SkillScopeStale, { link_target: target.folder });
    expect(detachedBy([target, linker])).toEqual([]);
  });
});
