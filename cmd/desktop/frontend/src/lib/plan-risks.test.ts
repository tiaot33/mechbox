// 更新页的风险：快照的键和更新计划的键对得上时，读过一次就直接用;打开过的更新计划读到的也算。
import { describe, expect, it } from "vitest";
import { PlannedChange, SourceType, UpdateCheckStatus, UpdateRiskKind, type Skill, type UpdatePlan } from "@bindings/internal/library";
import { knownRisks, ReadRetries, rememberPlanRisks, snapshotRiskKey } from "./plan-risks";

const skill = (id: number, version_id: number, check: Partial<NonNullable<Skill["last_update_check"]>>) =>
  ({ id, name: `s${id}`, version_id, source: { type: SourceType.SourceGitHub, label: "a/b" }, last_update_check: check }) as Skill;

describe("plan-risks", () => {
  // 继续保留的 doc 的结论停在更早的来源版本上，排在前面也不拿它定来源版本。
  const skills = [
    skill(7, 7, { status: UpdateCheckStatus.UpdateDeleted, revision: "old" }),
    skill(4, 26, { status: UpdateCheckStatus.UpdateAvailable, revision: "new", pending: true }),
    skill(5, 5, { status: UpdateCheckStatus.UpdateCurrent, revision: "new" }),
  ];
  const plan = {
    id: "p1",
    collection_id: 2,
    revision: "new",
    skills: [
      { skill: skills[1], change: PlannedChange.PlannedChanged, risks: [{ kind: UpdateRiskKind.RiskScriptChanged, path: "scripts/a.py" }] },
      { skill: skills[2], change: PlannedChange.PlannedUnchanged },
      { skill: skills[0], change: PlannedChange.PlannedDeleted },
    ],
  } as UpdatePlan;

  it("读过的更新计划按快照的键就能取到风险", () => {
    const key = rememberPlanRisks(plan);
    expect(key).toBe(snapshotRiskKey(2, skills));
    expect(knownRisks(snapshotRiskKey(2, skills), skills)).toEqual([{ kind: UpdateRiskKind.RiskScriptChanged, path: "scripts/a.py" }]);
  });

  it("跳过的改动(包括回滚掉的)的风险不算进这一行", () => {
    rememberPlanRisks(plan);
    const skipped = [skills[0], skill(4, 26, { status: UpdateCheckStatus.UpdateAvailable, revision: "new", skipped: true }), skills[2]];
    expect(knownRisks(snapshotRiskKey(2, skills), skipped)).toEqual([]);
  });

  it("调用被取消时拿到的空计划不记", () => {
    expect(rememberPlanRisks({} as UpdatePlan)).toBeUndefined();
  });
});

describe("ReadRetries", () => {
  it("读失败后等这个键自己的间隔才再读，最多读三次", () => {
    const r = new ReadRetries(2000, 3);
    r.started("a");
    r.failed("a", 0);
    // 别的集合 20 ms 后读完了，触发重算：a 还在等。
    expect(r.canRead("a", 20)).toBe(false);
    expect(r.canRead("b", 20)).toBe(true);
    expect(r.canRead("a", 2000)).toBe(true);
    r.started("a");
    r.failed("a", 2000);
    r.started("a");
    expect(r.canRead("a", 10_000)).toBe(false);
  });
});
