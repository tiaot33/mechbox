// 照着装：确认框里一处标题上的勾选、另外说出的改过与新版的技能，确认后逐处要做的事和结果怎么归并。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ErrorCode,
  GroupMemberOutcome,
  InstallPlanStatus,
  ScopeKind,
  SourceType,
  type Agent,
  type Group,
  type GroupMemberResult,
  type InstallPlan,
  type Scope,
  type Skill,
} from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import type { CopyPlace } from "@/lib/model";
import { canCheck, comingNotes, copyResults, copySteps, groupKey, meantPlaces, placeKeys, skillKey, togglePlace, type Plans } from "./copy-rules";

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
    const r = copyResults(s, steps(false), [{ results: groupResults }, {}, {}]);
    expect(r.installed).toBe(2);
    expect(r.groups).toBe(1);
    expect(r.rows.map((x) => x.label)).toEqual(["s3 → Claude Code · 全局"]);
    expect(r.placed.map((x) => x.skill_id)).toEqual([1, 1, 4]);
    expect(r.failed).toBe(false);
  });

  it("勾了覆盖还被跳过的成员照样列出", () => {
    const r = copyResults(s, steps(true), [{ results: groupResults }, {}, {}]);
    expect(r.rows.map((x) => x.label)).toEqual(["s2 → Claude Code · 全局", "s3 → Claude Code · 全局"]);
  });

  it("失败的写明原因、顶栏画叉;执行时才被占上的直接安装能点开占着的那个，组安装失败不记组安装数", () => {
    const r = copyResults(s, steps(false), [{ error: err(ErrorCode.Permission) }, { error: err(ErrorCode.FailedPrecondition, { skill_id: 3 }) }, {}]);
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
