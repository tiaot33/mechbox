// 更新页每一行的风险(改动了脚本等)来自更新计划。按"集合 + 来源版本 + 库里各技能的当前版本"记下来：同一份差异只读一次，
// 不论是更新页自己读的，还是用户打开过的更新计划，读到了就记下，更新页直接用。
import { PlannedChange, type CollectionID, type Skill, type UpdatePlan, type UpdateRisk } from "@bindings/internal/library";
import { pending } from "./model";

const risks = new Map<string, UpdateRisk[]>();

const keyOf = (id: CollectionID, revision: string | undefined, versions: number[]) =>
  `${id}:${revision ?? ""}:${[...versions].sort((a, b) => a - b).join(",")}`;

// snapshotRiskKey 是快照里一个集合现在该看哪一份风险：来源版本取要处理的技能看到的(更新页这一行写的就是它),
// 没有时再取集合里任一个技能看到的;继续保留的、跳过的技能的结论可能停在更早的来源版本上，不拿它们来定。
export function snapshotRiskKey(id: CollectionID, skills: Skill[]): string {
  const revision = (list: Skill[]) => list.map((x) => x.last_update_check?.revision).find(Boolean);
  return keyOf(id, revision(skills.filter(pending)) ?? revision(skills), skills.map((x) => x.version_id));
}

// planRiskKey 是一份更新计划对应的键，和快照的键对得上时就是同一份差异。
export function planRiskKey(plan: UpdatePlan): string {
  return keyOf(plan.collection_id, plan.revision, (plan.skills ?? []).map((p) => p.skill.version_id));
}

// rememberPlanRisks 记下一份更新计划里有变化(含推测出改名)的技能的风险，返回它的键。
// 没有 ID 的计划(调用被取消时绑定返回的空值)不记。
export function rememberPlanRisks(plan: UpdatePlan): string | undefined {
  if (!plan.id || !plan.collection_id) return undefined;
  const key = planRiskKey(plan);
  const movable = (plan.skills ?? []).filter((p) => p.change === PlannedChange.PlannedChanged || p.renamed_to);
  risks.set(key, movable.flatMap((p) => p.risks ?? []));
  return key;
}

export function knownRisks(key: string): UpdateRisk[] | undefined {
  return risks.get(key);
}
