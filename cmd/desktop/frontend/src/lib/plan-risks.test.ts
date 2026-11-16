// 更新页的风险：快照的键和更新计划的键对得上时，读过一次就直接用;打开过的更新计划读到的也算。
import { describe, expect, it } from "vitest";
import { PlannedChange, UpdateCheckStatus, UpdateRiskKind, type Skill, type UpdatePlan } from "@bindings/internal/library";
import { knownRisks, rememberPlanRisks, snapshotRiskKey } from "./plan-risks";

const skill = (id: number, version_id: number, check: Partial<NonNullable<Skill["last_update_check"]>>) =>
  ({ id, name: `s${id}`, version_id, last_update_check: check }) as Skill;

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
    expect(knownRisks(snapshotRiskKey(2, skills))).toEqual([{ kind: UpdateRiskKind.RiskScriptChanged, path: "scripts/a.py" }]);
  });

  it("调用被取消时拿到的空计划不记", () => {
    expect(rememberPlanRisks({} as UpdatePlan)).toBeUndefined();
  });
});
