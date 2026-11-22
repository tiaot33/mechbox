// 照着装：确认框里一处标题上的勾选、另外说出的改过与新版的技能，确认后逐处要做的事和结果怎么归并;
// 照着另一个 Agent、另一个项目装时装哪些，哪些未纳管技能带不过去。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ErrorCode,
  GroupMemberOutcome,
  InstallPlanStatus,
  ScopeKind,
  SkillScopeStatus,
  SourceType,
  type Agent,
  type Group,
  type GroupInstall,
  type GroupMemberResult,
  type InstallPlan,
  type RegisteredProject,
  type Scope,
  type Skill,
  type SkillScopeInspection,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { placeOf } from "@/lib/scope";
import {
  canCheck,
  checkedCounts,
  comingNotes,
  copyFromAgent,
  copyFromProject,
  copyProjectSources,
  copyResults,
  copySources,
  copySteps,
  groupKey,
  meantPlaces,
  placeKeys,
  skillKey,
  togglePlace,
  uncopiedProjectUnmanaged,
  uncopiedUnmanaged,
  type CopyPlace,
  type Plans,
} from "./copy-rules";

const claude = AgentCode.AgentClaudeCode;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const a = messages().actions;
const c = messages().copy;

const skill = (id: number) => ({ id, name: `s${id}`, source: { type: SourceType.SourceGitHub, label: "owner/repo" } }) as Skill;
const group = (id: number, members: number[]) => ({ id, name: `g${id}`, members }) as Group;
const plan = (status: InstallPlanStatus, extra: Partial<InstallPlan> = {}) => ({ status, name: "x", folder: "/home/.claude/skills/x", ...extra }) as InstallPlan;
const member = (skill_id: number, status: InstallPlanStatus) => ({ ...plan(status), skill_id }) as InstallPlan;
const err = (code: ErrorCode, details?: LibraryError["details"]): LibraryError => ({ code, message: code, details }) as LibraryError;

const where = (to: Scope, extra: Partial<CopyPlace> = {}): CopyPlace => ({ to, groups: [], skills: [], edited: [], newer: [], missing: false, ...extra });

const snapshot = () =>
  ({
    skills: [1, 2, 3, 4].map(skill),
    skillScopes: [],
    agents: [{ code: claude, name: "Claude Code" } as Agent],
    projects: [{ id: 7, name: "proj" }],
    info: { home_dir: "/home" },
  }) as unknown as Snapshot;

describe("确认框里的勾选", () => {
  const p = where(global(claude), { groups: [group(5, [3])], skills: [skill(1), skill(2)] });

  it("组安装读出计划就能勾，直接安装还要装得了", () => {
    expect(canCheck({ members: [] })).toBe(true);
    expect(canCheck({ plan: plan(InstallPlanStatus.InstallReady) })).toBe(true);
    expect(canCheck({ plan: plan(InstallPlanStatus.InstallOccupied) })).toBe(false);
    expect(canCheck({ error: err(ErrorCode.Permission) })).toBe(false);
    expect(canCheck(undefined)).toBe(false);
  });

  it("确认按钮上的技能数、组数按勾上的项算：直接安装的是技能，组安装的是组", () => {
    expect(checkedCounts(new Set([skillKey(p, 1), skillKey(p, 2), groupKey(p, 5)]))).toEqual({ skills: 2, groups: 1 });
    expect(checkedCounts(new Set())).toEqual({ skills: 0, groups: 0 });
  });

  it("一处标题：有勾上的就整处取消;一个都没勾时按默认规则整处勾上，有同名文件夹的、读不出计划的不勾", () => {
    const plans: Plans = new Map([
      [groupKey(p, 5), { members: [] }],
      [skillKey(p, 1), { plan: plan(InstallPlanStatus.InstallReady) }],
      [skillKey(p, 2), { plan: plan(InstallPlanStatus.InstallUntracked) }],
    ]);
    const keys = placeKeys(p);
    expect([...togglePlace(new Set(), keys, plans)]).toEqual([groupKey(p, 5), skillKey(p, 1)]);
    expect([...togglePlace(new Set([skillKey(p, 2), "other"]), keys, plans)]).toEqual(["other"]);
    expect([...togglePlace(new Set(), keys, null)]).toEqual([]);
  });

  it("另外说出的改过、新版的技能只算勾上的项会装过来的：直接安装勾上了，或随勾上的组装过来", () => {
    const edited = where(global(claude), { groups: [group(5, [3])], skills: [skill(1)], edited: [skill(1), skill(3)], newer: [skill(3)] });
    expect(comingNotes([edited], new Set())).toEqual({ edited: [], newer: [] });
    expect(comingNotes([edited], new Set([skillKey(edited, 1)]))).toEqual({ edited: ["s1"], newer: [] });
    expect(comingNotes([edited], new Set([groupKey(edited, 5)]))).toEqual({ edited: ["s3"], newer: ["s3"] });
  });

  it("结果只算默认勾的那几处，和用户勾了东西的那几处", () => {
    const here = where(project(claude, 7), { skills: [skill(1)] });
    const there = where(global(claude), { skills: [skill(2)] });
    const byDefault = (x: CopyPlace) => x === here;
    expect(meantPlaces([here, there], byDefault, new Set())).toEqual([here]);
    expect(meantPlaces([here, there], byDefault, new Set([skillKey(there, 2)]))).toEqual([here, there]);
  });
});

describe("确认后逐处要做的事", () => {
  const s = snapshot();

  it("项目不在这台机器上的一处整个跳过并说明", () => {
    const steps = copySteps(s, [where(project(claude, 7), { missing: true, skills: [skill(1)] })], new Map(), new Set(), new Set());
    expect(steps).toEqual([{ skip: { label: "Claude Code · proj", outcome: a.memberSkipped, tone: "warn", reason: c.missingPlace, occupier: undefined } }]);
  });

  it("每一处先组安装再直接安装;没勾的不装，读不出计划、被占用的照样写进结果", () => {
    const p = where(global(claude), { groups: [group(5, [3]), group(6, [4])], skills: [skill(1), skill(2), skill(3), skill(4)] });
    const plans: Plans = new Map([
      [groupKey(p, 5), { members: [member(3, InstallPlanStatus.InstallUntracked)] }],
      [groupKey(p, 6), { error: err(ErrorCode.Permission) }],
      [skillKey(p, 1), { plan: plan(InstallPlanStatus.InstallUntracked) }],
      [skillKey(p, 2), { plan: plan(InstallPlanStatus.InstallOccupied, { occupier_skill_id: 4 }) }],
      [skillKey(p, 3), { error: err(ErrorCode.Permission) }],
      [skillKey(p, 4), { plan: plan(InstallPlanStatus.InstallReady) }],
    ]);
    const steps = copySteps(s, [p], plans, new Set([groupKey(p, 5), skillKey(p, 1)]), new Set([groupKey(p, 5)]));
    const kind = (x: (typeof steps)[number]) => ("skip" in x ? `skip:${x.skip.label}` : "group" in x ? `group:${x.group.id}` : `skill:${x.skill.id}`);
    expect(steps.map(kind)).toEqual([
      "group:5",
      `skip:${c.groupFailed("g6", "Claude Code · 全局")}`,
      "skill:1",
      "skip:s2 → Claude Code · 全局",
      "skip:s3 → Claude Code · 全局",
    ]);
    expect(steps[0]).toMatchObject({ overwrite: true, untracked: new Set([3]) });
    expect(steps[2]).toMatchObject({ overwrite: true });
    expect("skip" in steps[3] && steps[3].skip.occupier).toBe(4);
  });
});

describe("照着装的结果怎么归并", () => {
  const s = snapshot();
  const p = where(global(claude), { groups: [group(5, [1, 2, 3])], skills: [skill(1), skill(4)] });
  const result = (skill_id: number, outcome: GroupMemberOutcome) => ({ skill_id, scope: global(claude), outcome }) as GroupMemberResult;
  const steps = (overwrite: boolean) =>
    copySteps(
      s,
      [p],
      new Map([
        [groupKey(p, 5), { members: [member(2, InstallPlanStatus.InstallUntracked)] }],
        [skillKey(p, 1), { plan: plan(InstallPlanStatus.InstallReady) }],
        [skillKey(p, 4), { plan: plan(InstallPlanStatus.InstallReady) }],
      ]),
      new Set([groupKey(p, 5), skillKey(p, 1), skillKey(p, 4)]),
      new Set(overwrite ? [groupKey(p, 5)] : []),
    );
  const groupResults = [
    result(1, GroupMemberOutcome.MemberInstalled),
    result(2, GroupMemberOutcome.MemberSkipped),
    result(3, GroupMemberOutcome.MemberSkipped),
  ];

  it("同一个技能既随组又直接安装时只算一次;有同名文件夹而没勾覆盖、被跳过的成员是用户选的，不再列出", () => {
    const r = copyResults(s, steps(false), [{ value: groupResults }, { value: [] }, { value: [] }]);
    expect(r.installed).toBe(2);
    expect(r.groups).toBe(1);
    expect(r.rows.map((x) => x.label)).toEqual(["s3 → Claude Code · 全局"]);
    expect(r.placed.map((x) => x.skill_id)).toEqual([1, 1, 4]);
    expect(r.failed).toBe(false);
  });

  it("勾了覆盖还被跳过的成员照样列出", () => {
    const r = copyResults(s, steps(true), [{ value: groupResults }, { value: [] }, { value: [] }]);
    expect(r.rows.map((x) => x.label)).toEqual(["s2 → Claude Code · 全局", "s3 → Claude Code · 全局"]);
  });

  it("失败的写明原因、顶栏画叉;执行时才被占上的直接安装能点开占着的那个，组安装失败不记组安装数", () => {
    const r = copyResults(s, steps(false), [{ error: err(ErrorCode.Permission) }, { error: err(ErrorCode.FailedPrecondition, { skill_id: 3 }) }, { value: [] }]);
    expect(r.rows.map((x) => [x.label, x.outcome, x.occupier])).toEqual([
      [c.groupFailed("g5", "Claude Code · 全局"), a.failed, undefined],
      ["s1 → Claude Code · 全局", a.failed, 3],
    ]);
    expect(r).toMatchObject({ installed: 1, groups: 0, failed: true });
  });

  it("还没执行到的不算", () => {
    expect(copyResults(s, steps(false), [])).toMatchObject({ rows: [], installed: 0, groups: 0, failed: false });
  });
});

describe("照着装时装哪些", () => {
  const snapshot = (s: Partial<Snapshot>) =>
    ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;
  const claude = AgentCode.AgentClaudeCode;
  const codex = AgentCode.AgentCodex;
  const cursor = "cursor" as AgentCode;
  const agents = [
    { code: claude, name: "Claude Code", global_directory: "/home/.claude/skills", project_directory: ".claude/skills" },
    { code: codex, name: "Codex", global_directory: "/home/.agents/skills", project_directory: ".agents/skills" },
    { code: cursor, name: "Cursor", global_directory: "/home/.cursor/skills", project_directory: ".agents/skills" },
  ] as Agent[];
  const at = (agent: AgentCode, project_id = 0): Scope =>
    project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
  const installed = (skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}) =>
    ({ skill_id, scope, folder: `f${skill_id}`, direct: true, status: SkillScopeStatus.SkillScopeCurrent, ...extra }) as SkillScopeInspection;
  const skill = (id: number) => ({ id, name: `s${id}`, source: { type: SourceType.SourceGitHub, label: "a/a" } }) as Skill;
  // 项目 1、3 在这台机器上，项目 2 不在。
  const projects = [{ id: 1, name: "web" }, { id: 2, name: "old", missing: true }, { id: 3, name: "new" }] as RegisteredProject[];
  const base = {
    agents,
    projects,
    skills: [1, 2, 3, 4, 5].map(skill),
    groups: [group(7, [4, 5])],
    groupInstalls: [] as GroupInstall[],
    unmanaged: [] as UnmanagedSkill[],
  };

  describe("照着另一个 Agent 装", () => {
    it("全局的装到全局，项目里的装到同一个项目;直接安装照样直接安装，组安装照样组安装", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude)),
          installed(4, at(claude), { direct: false, groups: [7] }),
          installed(5, at(claude), { direct: false, groups: [7] }),
          installed(2, at(claude, 1)),
        ],
        groupInstalls: [{ group_id: 7, scope: at(claude) }] as GroupInstall[],
      });
      const copy = copyFromAgent(s, claude, codex);
      expect(copy.map((p) => placeOf(p.to))).toEqual([0, 1]);
      expect(copy[0].skills.map((x) => x.id)).toEqual([1]);
      expect(copy[0].groups.map((g) => g.id)).toEqual([7]);
      expect(copy[1].skills.map((x) => x.id)).toEqual([2]);
      expect(copy[1].groups).toEqual([]);
    });

    it("只加不减：这里已经装了的技能、已有的组安装不列出", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude)), installed(2, at(claude)), installed(1, at(codex)), installed(3, at(codex))],
        groupInstalls: [{ group_id: 7, scope: at(claude) }, { group_id: 7, scope: at(codex) }] as GroupInstall[],
      });
      const copy = copyFromAgent(s, claude, codex);
      expect(copy).toHaveLength(1);
      expect(copy[0].skills.map((x) => x.id)).toEqual([2]);
      expect(copy[0].groups).toEqual([]);
    });

    it("读同一个目录的那一处本来就一样，不列出", () => {
      const s = snapshot({ ...base, skillScopes: [installed(1, at(codex, 1)), installed(2, at(codex))] });
      const copy = copyFromAgent(s, codex, cursor);
      expect(copy.map((p) => placeOf(p.to))).toEqual([0]);
    });

    it("组安装记在读同一个目录的另一个 Agent 上也算", () => {
      // Codex 与 Cursor 在项目里共用 .agents/skills,组安装记在 Codex 上;照着 Cursor 装到 Claude Code 时照样带上这个组。
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(4, at(codex, 1), { direct: false, groups: [7] }),
          installed(4, at(cursor, 1), { direct: false, groups: [7] }),
        ],
        groupInstalls: [{ group_id: 7, scope: at(codex, 1) }] as GroupInstall[],
      });
      expect(copyFromAgent(s, cursor, claude)).toMatchObject([{ to: { project_id: 1 }, groups: [{ id: 7 }], skills: [] }]);
      // 反过来照着 Claude Code 装到 Cursor:Cursor 那一处已经有这个组安装(记在 Codex 上),不再列出。
      const back = snapshot({
        ...base,
        skillScopes: [installed(4, at(claude, 1), { direct: false, groups: [7] })],
        groupInstalls: [{ group_id: 7, scope: at(claude, 1) }, { group_id: 7, scope: at(codex, 1) }] as GroupInstall[],
      });
      expect(copyFromAgent(back, claude, cursor)).toEqual([]);
    });

    it("项目里装到共用目录的代表 Agent 上，全局装到它自己", () => {
      const s = snapshot({ ...base, agentsShown: {}, skillScopes: [installed(1, at(claude)), installed(2, at(claude, 1))] });
      expect(copyFromAgent(s, claude, cursor).map((p) => p.to)).toEqual([at(cursor), at(codex, 1)]);
    });

    it("要装过来的技能里，原来那一处被改过的单列出来", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude), { status: SkillScopeStatus.SkillScopeModified }),
          installed(4, at(claude), { direct: false, groups: [7], status: SkillScopeStatus.SkillScopeModified }),
          installed(2, at(claude), { status: SkillScopeStatus.SkillScopeModified }),
          installed(2, at(codex)),
        ],
        groupInstalls: [{ group_id: 7, scope: at(claude) }] as GroupInstall[],
      });
      expect(copyFromAgent(s, claude, codex)[0].edited.map((x) => x.id)).toEqual([1, 4]);
    });

    it("原来那一处是在 app 外更新到的作者新版时不算改过，另列出来提示先更新", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude), { status: SkillScopeStatus.SkillScopeModified, source_latest: true }),
          installed(3, at(claude), { status: SkillScopeStatus.SkillScopeModified }),
        ],
      });
      const [place] = copyFromAgent(s, claude, codex);
      expect(place.edited.map((x) => x.id)).toEqual([3]);
      expect(place.newer.map((x) => x.id)).toEqual([1]);
    });

    it("项目不在这台机器上的那一处标出来", () => {
      const s = snapshot({ ...base, skillScopes: [installed(1, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing })] });
      expect(copyFromAgent(s, claude, codex)).toMatchObject([{ to: { project_id: 2 }, missing: true }]);
    });

    it("可以照着装的是有东西可装的其他 Agent,装得最多的在前", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude)),
          installed(4, at(claude)),
          installed(2, at(cursor)),
          installed(3, at(cursor)),
          installed(5, at(cursor)),
          installed(1, at(codex)),
        ],
      });
      expect(copySources(s, codex, 0).map((a) => a.code)).toEqual([cursor, claude]);
      expect(copySources(snapshot({ ...base, skillScopes: [installed(1, at(claude)), installed(1, at(codex))] }), codex, 0)).toEqual([]);
      const onlyMissing = snapshot({ ...base, skillScopes: [installed(1, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing })] });
      expect(copySources(onlyMissing, codex, 0)).toEqual([]);
    });

    it("从项目的作用域页打开时，在这个项目里可以照着装的最多的在前，一样多的再按装得多少排", () => {
      // Cursor 一共装得最多，但都在全局;Claude Code 在项目 1 里有 1 个技能和 1 个组安装可以照着装。
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(cursor)),
          installed(2, at(cursor)),
          installed(3, at(cursor)),
          installed(5, at(cursor)),
          installed(1, at(claude, 1)),
          installed(4, at(claude, 1), { direct: false, groups: [7] }),
        ],
        groupInstalls: [{ group_id: 7, scope: at(claude, 1) }] as GroupInstall[],
      });
      expect(copySources(s, codex, 0).map((a) => a.code)).toEqual([cursor, claude]);
      expect(copySources(s, codex, 1).map((a) => a.code)).toEqual([claude, cursor]);
      // 项目 3 里谁都没有可以照着装的：照旧装得最多的在前。
      expect(copySources(s, codex, 3).map((a) => a.code)).toEqual([cursor, claude]);
    });

    it("未纳管技能带不过来，数出来;读同一个目录的那几处不算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [],
        unmanaged: [{ scope: at(codex), name: "a" }, { scope: at(codex, 1), name: "b" }, { scope: at(claude), name: "c" }] as UnmanagedSkill[],
      });
      expect(uncopiedUnmanaged(s, codex, claude).unmanaged).toHaveLength(2);
      expect(uncopiedUnmanaged(s, codex, cursor).unmanaged).toHaveLength(1);
    });

    it("未纳管技能和作用域页同一个口径：归项目仓库管的单独数，指向 Agent 目录以外的不算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [],
        unmanaged: [
          { scope: at(claude), name: "mine" },
          { scope: at(claude, 1), name: "also-mine" },
          { scope: at(claude, 1), name: "team", in_repository: true },
          { scope: at(claude, 3), name: "locked", in_project_lock: true },
          { scope: at(claude), name: "outside", link_outside: true },
          { scope: at(claude, 1), name: "team-outside", in_repository: true, link_outside: true },
        ] as UnmanagedSkill[],
      });
      const left = uncopiedUnmanaged(s, claude, codex);
      expect(left.unmanaged.map((u) => u.name)).toEqual(["mine", "also-mine"]);
      expect(left.repo.map((u) => u.name)).toEqual(["team", "locked"]);
    });

    it("这一处已经有同名文件夹的未纳管技能不算带不过来", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude), { folder: "/home/.claude/skills/b" })],
        unmanaged: [
          { scope: at(codex), name: "a" },
          { scope: at(claude), name: "a" },
          { scope: at(codex), name: "b" },
          { scope: at(codex), name: "c" },
        ] as UnmanagedSkill[],
      });
      expect(uncopiedUnmanaged(s, codex, claude).unmanaged.map((u) => u.name)).toEqual(["c"]);
    });
  });

  describe("照着另一个项目装", () => {
    it("各 Agent 装到这个项目里的同一个 Agent;直接安装照样直接安装，组安装照样组安装，全局不算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude, 1)),
          installed(4, at(codex, 1), { direct: false, groups: [7] }),
          installed(5, at(codex, 1), { direct: false, groups: [7] }),
          installed(3, at(claude)),
        ],
        groupInstalls: [{ group_id: 7, scope: at(codex, 1) }] as GroupInstall[],
      });
      const copy = copyFromProject(s, 1, 3);
      expect(copy.map((p) => p.to)).toEqual([at(claude, 3), at(codex, 3)]);
      expect(copy[0].skills.map((x) => x.id)).toEqual([1]);
      expect(copy[0].groups).toEqual([]);
      expect(copy[1].skills).toEqual([]);
      expect(copy[1].groups.map((g) => g.id)).toEqual([7]);
    });

    it("项目里共用目录的几个 Agent 合成一处，组安装记在其中哪一个上都算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(2, at(codex, 1)), installed(2, at(cursor, 1)), installed(4, at(cursor, 1), { direct: false, groups: [7] })],
        groupInstalls: [{ group_id: 7, scope: at(cursor, 1) }] as GroupInstall[],
      });
      expect(copyFromProject(s, 1, 3)).toMatchObject([{ to: at(codex, 3), skills: [{ id: 2 }], groups: [{ id: 7 }] }]);
    });

    it("只加不减：这个项目里已经装了的技能、已有的组安装不列出", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude, 1)), installed(2, at(claude, 1)), installed(1, at(claude, 3)), installed(3, at(claude, 3))],
        groupInstalls: [{ group_id: 7, scope: at(codex, 1) }, { group_id: 7, scope: at(cursor, 3) }] as GroupInstall[],
      });
      expect(copyFromProject(s, 1, 3)).toMatchObject([{ to: at(claude, 3), skills: [{ id: 2 }], groups: [] }]);
      expect(copyFromProject(s, 3, 3)).toEqual([]);
    });

    it("要装过来的技能里，原来那个项目里被改过的单列出来", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude, 1), { status: SkillScopeStatus.SkillScopeModified }), installed(2, at(claude, 1))],
      });
      expect(copyFromProject(s, 1, 3)[0].edited.map((x) => x.id)).toEqual([1]);
    });

    it("可以照着装的是有东西可装的其他项目，装得最多的在前;不在这台机器上的项目也能照着", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude, 1)),
          installed(2, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing }),
          installed(3, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing }),
          installed(4, at(claude, 3)),
          installed(5, at(claude)),
        ],
      });
      expect(copyProjectSources(s, 3).map((p) => p.id)).toEqual([2, 1]);
      const done = snapshot({ ...base, skillScopes: [installed(1, at(claude, 1)), installed(1, at(claude, 3))] });
      expect(copyProjectSources(done, 3)).toEqual([]);
    });

    it("装得多少按技能算：共用目录的几个 Agent 的同一个技能只算一次", () => {
      // 项目 1 在 .agents/skills 里装了 1 个(Codex、Cursor 各列一条),项目 2 的 Claude Code 装了 2 个。
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(codex, 1)), installed(1, at(cursor, 1)), installed(2, at(claude, 2)), installed(3, at(claude, 2))],
      });
      expect(copyProjectSources(s, 3).map((p) => p.id)).toEqual([2, 1]);
    });

    it("只有空的组安装、没装库里技能的项目不能照着装", () => {
      const s = snapshot({ ...base, groups: [group(8, [])], skillScopes: [], groupInstalls: [{ group_id: 8, scope: at(claude, 1) }] as GroupInstall[] });
      expect(copyProjectSources(s, 3)).toEqual([]);
    });

    it("未纳管技能按作用域页的口径数：归项目仓库管的单独算，指向 Agent 目录以外的不算，这里已有同名文件夹的不算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude, 3), { folder: "/p/new/.claude/skills/taken" })],
        unmanaged: [
          { scope: at(claude, 1), name: "mine" },
          { scope: at(codex, 1), name: "also-mine" },
          { scope: at(claude, 1), name: "team", in_repository: true },
          { scope: at(claude, 1), name: "locked", in_project_lock: true },
          { scope: at(claude, 1), name: "author", link_outside: true },
          { scope: at(claude, 1), name: "author-locked", link_outside: true, in_project_lock: true },
          { scope: at(claude, 1), name: "taken" },
          { scope: at(codex, 1), name: "there" },
          { scope: at(codex, 3), name: "there" },
          { scope: at(claude), name: "global" },
        ] as UnmanagedSkill[],
      });
      const left = uncopiedProjectUnmanaged(s, 1, 3);
      expect(left.unmanaged.map((u) => u.name)).toEqual(["mine", "also-mine"]);
      expect(left.repo.map((u) => u.name)).toEqual(["team", "locked"]);
    });
  });
});
