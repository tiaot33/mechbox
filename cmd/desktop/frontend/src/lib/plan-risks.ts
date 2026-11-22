// 更新页每一行的风险(改动了脚本等)来自更新计划。按"集合 + 来源版本 + 库里各技能的当前版本"记下来：同一份差异只读一次，
// 不论是更新页自己读的，还是用户打开过的更新计划，读到了就记下，更新页直接用。
import { PlannedChange, type CollectionID, type Skill, type SkillID, type UpdatePlan, type UpdateRisk } from "@bindings/internal/library";
import { sourceChanges } from "./source-state";

const risks = new Map<string, Map<SkillID, UpdateRisk[]>>();

const keyOf = (id: CollectionID, revision: string | undefined, versions: number[]) =>
  `${id}:${revision ?? ""}:${[...versions].sort((a, b) => a - b).join(",")}`;

// snapshotRiskKey 是快照里一个集合现在该看哪一份风险：来源版本取要处理的技能看到的(更新页这一行写的就是它),
// 没有时再取集合里任一个技能看到的;继续保留的、跳过的技能的结论可能停在更早的来源版本上，不拿它们来定。
export function snapshotRiskKey(id: CollectionID, skills: Skill[]): string {
  const revision = (list: Skill[]) => list.map((x) => x.last_update_check?.revision).find(Boolean);
  return keyOf(id, revision(sourceChanges(skills).todo) ?? revision(skills), skills.map((x) => x.version_id));
}

// planRiskKey 是一份更新计划对应的键，和快照的键对得上时就是同一份差异。
export function planRiskKey(plan: UpdatePlan): string {
  return keyOf(plan.collection_id, plan.revision, (plan.skills ?? []).map((p) => p.skill.version_id));
}

// rememberPlanRisks 按技能记下一份更新计划里有变化(含推测出改名)的技能的风险，返回它的键。跳过了的也记:
// 要不要算进这一行，取的时候按快照里它现在是不是要处理的定(knownRisks)。
// 没有 ID 的计划(调用被取消时绑定返回的空值)不记。
export function rememberPlanRisks(plan: UpdatePlan): string | undefined {
  if (!plan.id || !plan.collection_id) return undefined;
  const key = planRiskKey(plan);
  const movable = (plan.skills ?? []).filter((p) => p.change === PlannedChange.PlannedChanged || p.renamed_to);
  risks.set(key, new Map(movable.map((p) => [p.skill.id, p.risks ?? []])));
  return key;
}

// knownRisks 是这一行要算的风险：只算快照里现在有变化的技能(见 lib/source-state),跳过的、回滚掉的不算;这个键还没读过时为 undefined。
export function knownRisks(key: string, skills: Skill[]): UpdateRisk[] | undefined {
  const bySkill = risks.get(key);
  return bySkill && sourceChanges(skills).changed.flatMap((x) => bySkill.get(x.id) ?? []);
}

// ReadRetries 管更新页读风险的重读：每个键最多读 max 次;读失败后要等这个键自己的 delay 才能再读，
// 别的集合读完触发的重算不能提前重读它。
export class ReadRetries {
  private tries = new Map<string, number>();
  private retryAt = new Map<string, number>();
  constructor(
    private delay: number,
    private max: number,
  ) {}
  canRead(key: string, now: number): boolean {
    return (this.tries.get(key) ?? 0) < this.max && (this.retryAt.get(key) ?? 0) <= now;
  }
  started(key: string) {
    this.tries.set(key, (this.tries.get(key) ?? 0) + 1);
  }
  failed(key: string, now: number) {
    this.retryAt.set(key, now + this.delay);
  }
}
