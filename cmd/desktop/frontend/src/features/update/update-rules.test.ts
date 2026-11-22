// 更新流程里的判断：重新读取是更新、补装还是转去更新计划，补装时哪几处算进那次更新，汇报给不给回滚、重试怎么分批，
// 更新计划弹窗列哪几节、要不要提醒留在旧版本，以及一次更新的结果怎么汇总。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ErrorCode,
  PlannedChange,
  ReinstallOutcome,
  ScopeKind,
  SkillScopeStatus,
  SourceType,
  UpdateRecordKind,
  type Agent,
  type PlannedSkill,
  type ReinstallRequest,
  type ReinstallResult,
  type Scope,
  type Skill,
  type SkillScopeInspection,
  type UpdatePlan,
  type UpdateRecord,
  type UpdateResult,
} from "@bindings/internal/library";
import { placeKey } from "@/lib/consent";
import type { LibraryError, Snapshot } from "@/lib/library";
import {
  currentRecord,
  directoryReread,
  failedOnly,
  movedSince,
  planSections,
  plannedName,
  refreshOutcome,
  rereadChange,
  retryBatches,
  retryRequests,
  rollbackOffer,
  sameFolderAsSource,
  splitByRecord,
  stalePlan,
  stillFailed,
  updateOutcome,
  type UpdateOutcome,
} from "./update-rules";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });

const skill = (id: number, extra: Partial<Skill> = {}) => ({ id, name: `s${id}`, version_id: 100 + id, ...extra }) as Skill;
const planned = (id: number, change: PlannedChange, extra: Partial<PlannedSkill> = {}): PlannedSkill => ({ skill: skill(id), change, ...extra });
const plan = (skills: PlannedSkill[], extra: Partial<UpdatePlan> = {}): UpdatePlan =>
  ({ id: "plan-1", collection_id: 1, source: { type: SourceType.SourceGitHub, label: "owner/repo" }, skills, installs: [], ...extra }) as UpdatePlan;
const request = (skill_id: number, scope: Scope): ReinstallRequest => ({ skill_id, scope, overwrite: false });
const result = (skill_id: number, scope: Scope, outcome: ReinstallOutcome, error?: Partial<LibraryError>) =>
  ({ skill_id, scope, outcome, error }) as ReinstallResult;
const err = (code: ErrorCode): LibraryError => ({ code, message: code }) as LibraryError;

// 项目里 Claude Code 和 Codex 读同一个目录：合成一处。
const snapshot = (s: Partial<Snapshot> = {}) =>
  ({
    skills: [],
    skillScopes: [],
    agents: [{ code: claude, name: "Claude Code", project_directories: [".agents" ]} as Agent, { code: codex, name: "Codex", project_directories: [".agents" ]} as Agent],
    projects: [{ id: 7, name: "proj" }],
    info: { home_dir: "/home" },
    ...s,
  }) as unknown as Snapshot;

describe("更新计划用不了了", () => {
  it("生成之后来源或库变了、勾选的技能没了时让用户重新生成;别的错误照常报", () => {
    expect(stalePlan(err(ErrorCode.Conflict))).toBe(true);
    expect(stalePlan(err(ErrorCode.NotFound))).toBe(true);
    expect(stalePlan(err(ErrorCode.Permission))).toBe(false);
  });
});

describe("一次更新的结果怎么汇总", () => {
  const kept = { requests: [request(1, global(claude))], keptEdited: 2, keptMissing: 1 };
  const updated = (installs: ReinstallResult[]): UpdateResult =>
    ({ record: 9, skills: [{ skill_id: 1, changed: true }, { skill_id: 2, changed: false }], installs }) as unknown as UpdateResult;

  it("按集合查记录;项目里共用一个文件夹的几处算一处，没换上的单列", () => {
    const o = updateOutcome(
      snapshot(),
      plan([]),
      "owner/repo",
      new Set([1, 2]),
      kept,
      updated([
        result(1, project(claude, 7), ReinstallOutcome.Reinstalled),
        result(1, project(codex, 7), ReinstallOutcome.Reinstalled),
        result(1, global(claude), ReinstallOutcome.ReinstallFailed),
      ]),
    );
    expect(o).toMatchObject({ label: "owner/repo", local: false, record: 9, history: { collection_id: 1 }, changed: 1, placed: 1, keptEdited: 2, keptMissing: 1 });
    expect(o.failed.map((r) => r.scope)).toEqual([global(claude)]);
    expect(o.requests).toBe(kept.requests);
    expect(o.plain.size).toBe(0);
  });

  it("没有来源的技能按技能查;本地目录来源记为本地", () => {
    const local = plan([], { collection_id: 0, source: { type: SourceType.SourceLocal, label: "/src" } as UpdatePlan["source"] });
    const o = updateOutcome(snapshot(), local, "s3", new Set([3]), kept, updated([]), "Claude Code · 全局");
    expect(o).toMatchObject({ local: true, history: { skill_id: 3 }, from: "Claude Code · 全局" });
  });
});

describe("重新读取一个本地目录里的技能", () => {
  it("目录里没有了的、删掉的不读;没变的只补装;改过的读出来成为新版本", () => {
    const p = plan([planned(1, PlannedChange.PlannedDeleted), planned(2, PlannedChange.PlannedUnchanged), planned(3, PlannedChange.PlannedChanged)]);
    expect(rereadChange(p, 1)).toBe("gone");
    expect(rereadChange(p, 4)).toBe("gone");
    expect(rereadChange(p, 2)).toBe("unchanged");
    expect(rereadChange(p, 3)).toBe("changed");
  });
});

describe("重新读取整个目录", () => {
  it("有删掉、改名或会换掉用户修改的时转去更新计划由用户拿主意", () => {
    expect(directoryReread(plan([planned(1, PlannedChange.PlannedDeleted)]))).toEqual({ kind: "decide" });
    expect(directoryReread(plan([planned(1, PlannedChange.PlannedChanged, { renamed_to: "t" })]))).toEqual({ kind: "decide" });
    expect(directoryReread(plan([planned(1, PlannedChange.PlannedChanged, { edited: true })]))).toEqual({ kind: "decide" });
  });

  it("照默认勾选读得到的直接读;改过的都默认不读时说没有默认读进来的，完全一样时说没变", () => {
    expect(directoryReread(plan([planned(1, PlannedChange.PlannedChanged), planned(2, PlannedChange.PlannedUnchanged)]))).toEqual({
      kind: "update",
      checked: new Set([1]),
    });
    expect(directoryReread(plan([planned(1, PlannedChange.PlannedChanged, { skipped: true })]))).toEqual({ kind: "nothing", changed: true });
    expect(directoryReread(plan([planned(1, PlannedChange.PlannedUnchanged)]))).toEqual({ kind: "nothing", changed: false });
  });
});

describe("目录没变时补装：哪几处算进带来当前版本的那次更新", () => {
  const record = (id: number, extra: Partial<UpdateRecord>) =>
    ({ id, kind: UpdateRecordKind.UpdateRecordUpdate, skills: [{ skill_id: 1, from_version_id: 100, to_version_id: 101 }], installs: [], ...extra }) as UpdateRecord;

  it("找把技能换到当前版本、没被回滚的那次更新;跳过、版本没变、已回滚的不算", () => {
    const s1 = skill(1);
    const history = [
      record(1, { kind: UpdateRecordKind.UpdateRecordSkip }),
      record(2, { rolled_back_by: 5 }),
      record(3, { skills: [{ skill_id: 1, from_version_id: 101, to_version_id: 101 }] as UpdateRecord["skills"] }),
      record(4, {}),
    ];
    expect(currentRecord(history, s1)?.id).toBe(4);
    expect(currentRecord(history.slice(0, 3), s1)).toBeUndefined();
    expect(currentRecord(history, skill(1, { version_id: 999 }))).toBeUndefined();
  });

  it("那次更新列过的算进它，其余是普通重新安装;之后又换过版本(Conflict)的改按普通重新安装", () => {
    const listed = record(4, { installs: [{ skill_id: 1, scope: global(claude) }] as UpdateRecord["installs"] });
    const requests = [request(1, global(claude)), request(1, global(codex))];
    const { viaRecord, plain } = splitByRecord(requests, listed);
    expect(viaRecord).toEqual([request(1, global(claude))]);
    expect(plain).toEqual([request(1, global(codex))]);
    expect(splitByRecord(requests, undefined)).toEqual({ viaRecord: [], plain: requests });

    const moved = movedSince(requests, [result(1, global(claude), ReinstallOutcome.ReinstallFailed, { code: ErrorCode.Conflict }), result(1, global(codex), ReinstallOutcome.Reinstalled)]);
    expect(moved.moved).toEqual([request(1, global(claude))]);
    expect(moved.results.map((r) => r.scope)).toEqual([global(codex)]);
  });

  it("补装的汇总不换版本，没算进那次更新的地方记为 plain", () => {
    const kept = { requests: [request(1, global(claude)), request(1, global(codex))], keptEdited: 0, keptMissing: 1 };
    const o = refreshOutcome(snapshot(), skill(1), record(4, {}), kept, [result(1, global(claude), ReinstallOutcome.Reinstalled), result(1, global(codex), ReinstallOutcome.ReinstallFailed)], [request(1, global(codex))]);
    expect(o).toMatchObject({ label: "s1", local: true, record: 4, history: { skill_id: 1 }, changed: 0, placed: 1, keptMissing: 1 });
    expect(o.failed.map((r) => r.scope)).toEqual([global(codex)]);
    expect([...o.plain]).toEqual([placeKey(request(1, global(codex)))]);
    expect(refreshOutcome(snapshot(), skill(1), undefined, kept, [], []).record).toBe(0);
  });
});

describe("汇报与重试", () => {
  const outcome = (extra: Partial<UpdateOutcome>): UpdateOutcome => ({
    label: "c",
    local: false,
    record: 9,
    history: { collection_id: 1 },
    changed: 1,
    placed: 0,
    keptEdited: 0,
    keptMissing: 0,
    failed: [],
    requests: [],
    plain: new Set(),
    ...extra,
  });

  it("只汇报一次换了新版本的更新时给回滚;几次一起、只补装、没有记录的不给", () => {
    const o = outcome({});
    expect(rollbackOffer([o])).toBe(o);
    expect(rollbackOffer([o, outcome({})])).toBeUndefined();
    expect(rollbackOffer([outcome({ changed: 0 })])).toBeUndefined();
    expect(rollbackOffer([outcome({ record: 0 })])).toBeUndefined();
  });

  it("重试只换没换上的;算进那次更新的一批带记录，plain 里的和没有记录的按普通重新安装", () => {
    const requests = [request(1, global(claude)), request(2, global(claude)), request(3, global(claude))];
    const failed = [result(1, global(claude), ReinstallOutcome.ReinstallFailed), result(2, global(claude), ReinstallOutcome.ReinstallFailed)];
    const o = outcome({ requests, plain: new Set([placeKey(requests[1])]) });
    expect(retryRequests(o, failed)).toEqual(requests.slice(0, 2));
    expect(retryBatches(o, failed)).toEqual([
      { record: 9, requests: [requests[0]] },
      { record: 0, requests: [requests[1]] },
    ]);
    expect(retryBatches(outcome({ requests, record: 0 }), failed)).toEqual([{ record: 0, requests: requests.slice(0, 2) }]);
    expect(retryBatches(o, [])).toEqual([]);
  });

  it("一批重试之后：调用成功时看结果，调用失败(含取消)时这一批原来没换上的照旧算没换上", () => {
    const failed = [result(1, global(claude), ReinstallOutcome.ReinstallFailed), result(2, global(claude), ReinstallOutcome.ReinstallFailed)];
    const batch = [request(1, global(claude))];
    expect(stillFailed(failed, batch, [result(1, global(claude), ReinstallOutcome.Reinstalled)])).toEqual([]);
    expect(stillFailed(failed, batch, undefined)).toEqual([failed[0]]);
    expect(failedOnly(null)).toEqual([]);
  });
});

describe("更新计划弹窗里的几节", () => {
  const p = plan([
    planned(1, PlannedChange.PlannedChanged),
    planned(2, PlannedChange.PlannedChanged, { edited: true }),
    planned(3, PlannedChange.PlannedDeleted, { renamed_to: "t3" }),
    planned(4, PlannedChange.PlannedDeleted),
    planned(5, PlannedChange.PlannedUnchanged),
    planned(6, PlannedChange.PlannedChanged),
  ]);

  it("推测出改名的不算删除，已移除的不再列;只看一个技能时其余有变化的收起来", () => {
    const r = planSections(p, new Set([1]), new Set([4]), false, 6);
    expect(r.renamed.map((x) => x.skill.id)).toEqual([3]);
    expect(r.deleted).toEqual([]);
    expect(r.unchanged.map((x) => x.skill.id)).toEqual([5]);
    expect(r.shown.map((x) => x.skill.id)).toEqual([1, 6]);
    expect(r.folded.map((x) => x.skill.id)).toEqual([2]);
    expect(planSections(p, new Set(), new Set(), true).folded).toEqual([]);
  });

  it("没勾上的有变化、推测出改名的会留在旧版本上时提醒;只是没勾用户改过的不提醒", () => {
    expect(planSections(p, new Set([1, 3, 6]), new Set(), true).splitting).toBe(false);
    expect(planSections(p, new Set([1, 3]), new Set(), true).splitting).toBe(true);
    expect(planSections(p, new Set(), new Set(), true).splitting).toBe(false);
    expect(planSections(plan([planned(1, PlannedChange.PlannedChanged)]), new Set([9]), new Set(), true).splitting).toBe(false);
  });

  it("勾上的推测出改名的写成旧名 → 新名", () => {
    expect(plannedName(p, new Set([3]), 3)).toBe("s3 → t3");
    expect(plannedName(p, new Set(), 3)).toBe("s3");
    expect(plannedName(p, new Set(), 99)).toBe("#99");
  });
});

describe("用目录里的修改更新：哪些地方已经是这份修改", () => {
  const place = (scope: Scope): SkillScopeInspection => ({ skill_id: 1, scope, directory: "", folder: "/f", direct: true, status: SkillScopeStatus.SkillScopeCurrent });

  it("计划里不列的其他地方算;取内容的这一处、和它合成一处的、计划里列着的不算", () => {
    const s = snapshot({ skillScopes: [place(project(claude, 7)), place(project(codex, 7)), place(global(claude)), place(global(codex))] });
    expect(sameFolderAsSource(s, 1, project(claude, 7), [place(global(claude))]).map((p) => p.scope)).toEqual([global(codex)]);
  });
});
