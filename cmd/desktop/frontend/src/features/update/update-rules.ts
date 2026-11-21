// 更新流程里的判断：更新计划弹窗(update-dialog.tsx)列哪几节、要不要提醒留在旧版本，
// 本地目录来源的"重新读取"(update.tsx)是更新、补装还是转去更新计划由用户拿主意，补装时哪几处算进带来当前版本的那次更新，
// 更新完成后的汇报(report.tsx)给不给回滚、重试时怎么分批，以及一次更新的结果怎么汇总(UpdateOutcome)。
// 默认勾哪些技能、哪些地方见 lib/consent.ts。不碰界面、不调技能库，单元测试见 update-rules.test.ts。
import {
  ErrorCode,
  PlannedChange,
  ReinstallOutcome,
  UpdateRecordKind,
  type ReinstallRequest,
  type ReinstallResult,
  type Scope,
  type Skill,
  type SkillID,
  type SkillScopeInspection,
  type UpdateHistoryFilter,
  type UpdatePlan,
  type UpdateRecord,
  type UpdateRecordID,
  type UpdateResult,
} from "@bindings/internal/library";
import { defaultSkills, placeKey } from "@/lib/consent";
import type { LibraryError, Snapshot } from "@/lib/library";
import { isLocal, mergeProjectPlaces, sameMergedScope, scopeKey, skillScopesOf } from "@/lib/model";

export interface UpdateOutcome {
  label: string;
  local: boolean;
  from?: string; // 用目录里的修改更新时取内容的那一处，如"Claude Code · 全局"
  record: UpdateRecordID; // 这次更新的记录;库里没有新版本时为 0,没有可回滚的
  history: UpdateHistoryFilter; // 在哪里查得到这条记录(回滚时读出它，列出会换回什么)
  changed: number; // 换了新版本的技能数
  placed: number; // 换成新版的地方
  keptEdited: number; // 用户改过、没勾，保持原样的地方
  keptMissing: number; // 文件夹不在了、没勾，没装回去的地方
  failed: ReinstallResult[];
  requests: ReinstallRequest[];
  plain: Set<string>; // 不算进 record 的地方(placeKey),没换上时按普通重新安装重试
}

// Kept 是一次更新或补装要换的地方(requests),以及没勾、保持原样的文件夹数(见 consent 的 updateInstalls / refreshRequests)。
export type Kept = { requests: ReinstallRequest[]; keptEdited: number; keptMissing: number };

// stalePlan 是更新计划用不了了：生成之后来源或库变了(Conflict),句柄已失效，或勾选的技能在来源里没了、在库里被移除了(NotFound)。
// 这时让用户重新生成更新计划，而不是报错。
export function stalePlan(err: LibraryError): boolean {
  return err.code === ErrorCode.Conflict || err.code === ErrorCode.NotFound;
}

// failedOnly 是逐处换的结果里没换上的。
export const failedOnly = (results: ReinstallResult[] | null) => (results ?? []).filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed);

// updateOutcome 汇总一次更新(Update)的结果:按集合查得到记录，没有来源的技能按技能查;换上的地方里项目中共用一个文件夹的几处算一处。
export function updateOutcome(s: Snapshot, plan: UpdatePlan, label: string, checked: Set<SkillID>, kept: Kept, result: UpdateResult, from?: string): UpdateOutcome {
  const installs = result.installs ?? [];
  return {
    label,
    local: isLocal(plan.source),
    from,
    record: result.record ?? 0,
    history: plan.collection_id ? { collection_id: plan.collection_id } : { skill_id: [...checked][0] },
    changed: (result.skills ?? []).filter((x) => x.changed).length,
    placed: mergeProjectPlaces(s, installs.filter((r) => r.outcome !== ReinstallOutcome.ReinstallFailed)).length,
    keptEdited: kept.keptEdited,
    keptMissing: kept.keptMissing,
    failed: failedOnly(installs),
    requests: kept.requests,
    plain: new Set(),
  };
}

// ---------- 重新读取(本地目录来源) ----------

// rereadChange 是逐个技能"重新读取"时目录里的这个技能怎样了:gone 是目录里没有了(删掉或改了名),
// unchanged 是和库里一样(只把还没换上的地方换成当前版本),changed 是改过(读出来成为新版本)。
export function rereadChange(plan: UpdatePlan, id: SkillID): "gone" | "unchanged" | "changed" {
  const item = plan.skills?.find((p) => p.skill.id === id);
  if (!item || item.change === PlannedChange.PlannedDeleted) return "gone";
  return item.change === PlannedChange.PlannedUnchanged ? "unchanged" : "changed";
}

// directoryReread 是整个目录"重新读取"怎么做:有要用户拿主意的(目录里删掉或改了名、会换掉库里用户修改的)时 decide,转去更新计划;
// 照默认勾选读得到的(见 consent 的 defaultSkills)就 update;都读不进来时 nothing,changed 是目录里有没有改过的
// (有改过的只是默认不读，如回滚掉的),没有时目录和库里完全一样。
export function directoryReread(plan: UpdatePlan): { kind: "decide" } | { kind: "nothing"; changed: boolean } | { kind: "update"; checked: Set<SkillID> } {
  const items = plan.skills ?? [];
  if (items.some((p) => p.change === PlannedChange.PlannedDeleted || !!p.renamed_to || (p.edited && p.change === PlannedChange.PlannedChanged))) return { kind: "decide" };
  const checked = defaultSkills(plan);
  if (checked.size) return { kind: "update", checked };
  return { kind: "nothing", changed: items.some((p) => p.change !== PlannedChange.PlannedUnchanged) };
}

// currentRecord 是带来技能当前版本的那次更新：没被回滚、把这个技能换到了它现在的版本(版本真的变了)。没有时为 undefined。
export function currentRecord(history: UpdateRecord[], skill: Skill): UpdateRecord | undefined {
  return history.find(
    (r) =>
      r.kind === UpdateRecordKind.UpdateRecordUpdate &&
      !r.rolled_back_by &&
      (r.skills ?? []).some((x) => x.skill_id === skill.id && x.to_version_id === skill.version_id && x.from_version_id !== x.to_version_id),
  );
}

// splitByRecord 把补装的几处分开:viaRecord 是那次更新里列过的(补装算进那次更新，回滚它时一起装回去),plain 是普通的重新安装。
export function splitByRecord(requests: ReinstallRequest[], record: UpdateRecord | undefined) {
  const listed = new Set((record?.installs ?? []).map(placeKey));
  return { viaRecord: requests.filter((r) => listed.has(placeKey(r))), plain: requests.filter((r) => !listed.has(placeKey(r))) };
}

// movedSince 是算进那次更新补装之后：那次更新之后技能又换过版本(更新后又回滚)的地方那次更新已管不到(Conflict),
// moved 是这几处，改按普通重新安装换上;results 是其余的结果。results 与 viaRecord 一一对应。
export function movedSince(viaRecord: ReinstallRequest[], results: ReinstallResult[]) {
  const moved = (i: number) => results[i]?.error?.code === ErrorCode.Conflict;
  return { results: results.filter((_, i) => !moved(i)), moved: viaRecord.filter((_, i) => moved(i)) };
}

// refreshOutcome 汇总目录内容没变时"重新读取"的补装:没有换新版本(changed 为 0),plain 是没算进那次更新(record)的地方。
export function refreshOutcome(s: Snapshot, skill: Skill, record: UpdateRecord | undefined, kept: Kept, results: ReinstallResult[], plain: ReinstallRequest[]): UpdateOutcome {
  return {
    label: skill.name,
    local: true,
    record: record?.id ?? 0,
    history: { skill_id: skill.id },
    changed: 0,
    placed: mergeProjectPlaces(s, results.filter((r) => r.outcome !== ReinstallOutcome.ReinstallFailed)).length,
    keptEdited: kept.keptEdited,
    keptMissing: kept.keptMissing,
    failed: failedOnly(results),
    requests: kept.requests,
    plain: new Set(plain.map(placeKey)),
  };
}

// ---------- 汇报与重试 ----------

// rollbackOffer 是汇报里给"回滚"的那次更新：只汇报一次更新、它有记录、换了新版本时才给;只补装、没换版本的(目录没变时的重新读取)
// 没有可退回的改动。
export function rollbackOffer(outcomes: UpdateOutcome[]): UpdateOutcome | undefined {
  const o = outcomes.length === 1 ? outcomes[0] : undefined;
  return o && o.record && o.changed ? o : undefined;
}

// retryRequests 是一次更新里还没换上(failed)的地方要再换的请求。
export function retryRequests(o: UpdateOutcome, failed: ReinstallResult[]): ReinstallRequest[] {
  return o.requests.filter((r) => failed.some((f) => placeKey(f) === placeKey(r)));
}

// retryBatches 是重试一次更新里没换上的地方时分的批：算进那次更新的(有记录、不在 plain 里)一批，带记录;其余按普通重新安装一批(record 为 0)。
// 空的批不算。
export function retryBatches(o: UpdateOutcome, failed: ReinstallResult[]): { record: UpdateRecordID; requests: ReinstallRequest[] }[] {
  const again = retryRequests(o, failed);
  const viaRecord = o.record ? again.filter((r) => !o.plain.has(placeKey(r))) : [];
  return [
    { record: o.record, requests: viaRecord },
    { record: 0, requests: again.filter((r) => !viaRecord.includes(r)) },
  ].filter((b) => b.requests.length > 0);
}

// stillFailed 是重试一批之后还没换上的:results 是这一批的结果;调用失败(含取消)时为 null,这一批原来没换上的照旧算没换上。
export function stillFailed(failed: ReinstallResult[], batch: ReinstallRequest[], results: ReinstallResult[] | null | undefined): ReinstallResult[] {
  return results ? failedOnly(results) : failed.filter((f) => batch.some((r) => placeKey(r) === placeKey(f)));
}

// ---------- 更新计划弹窗 ----------

// planSections 是更新计划弹窗里的几节:changed 有变化，renamed 推测出改名，deleted 来源里已删除(推测出改名的、已移除的 removed 不算),
// unchanged 没变化;只看一个技能(focus)、没展开时有变化的只列它和勾上的，其余收进 folded。movable 是勾上就会换成新版的(有变化、推测出改名)。
// splitting 是要不要提醒：还没勾上的有变化或推测出改名的技能会留在旧的来源版本上;更新会换掉用户修改的(edited)默认就不勾，
// 来源版本也可能本来就不知道，留着它们是预料之中的，不为此警告。
export function planSections(plan: UpdatePlan, checked: ReadonlySet<SkillID>, removed: ReadonlySet<SkillID>, expanded: boolean, focus?: SkillID) {
  const skills = plan.skills ?? [];
  const changed = skills.filter((p) => p.change === PlannedChange.PlannedChanged);
  const renamed = skills.filter((p) => !!p.renamed_to);
  const shown = expanded ? changed : changed.filter((p) => p.skill.id === focus || checked.has(p.skill.id));
  const movable = [...changed, ...renamed];
  return {
    skills,
    changed,
    renamed,
    deleted: skills.filter((p) => p.change === PlannedChange.PlannedDeleted && !p.renamed_to && !removed.has(p.skill.id)),
    unchanged: skills.filter((p) => p.change === PlannedChange.PlannedUnchanged),
    shown,
    folded: changed.filter((p) => !shown.includes(p)),
    movable,
    splitting: skills.length > 1 && checked.size > 0 && movable.some((p) => !checked.has(p.skill.id) && !p.edited),
  };
}

// plannedName 是更新计划里一个技能的写法：勾上的推测出改名的写成"旧名 → 新名";不在计划里的写成 #ID。
export function plannedName(plan: UpdatePlan, checked: ReadonlySet<SkillID>, id: SkillID): string {
  const p = (plan.skills ?? []).find((x) => x.skill.id === id);
  if (!p) return `#${id}`;
  return p.renamed_to && checked.has(id) ? `${p.skill.name} → ${p.renamed_to}` : p.skill.name;
}

// ---------- 用目录里的修改更新 ----------

// sameFolderAsSource 是用 scope 那一处的修改更新时，计划里不列(places 里没有)、和取内容的这一处是同一个文件夹的其他地方
// (共用目录，或符号链接指向同一个文件夹):内容已经是这份修改。
export function sameFolderAsSource(s: Snapshot, id: SkillID, scope: Scope, places: SkillScopeInspection[]): SkillScopeInspection[] {
  return skillScopesOf(s, id).filter((p) => !sameMergedScope(s, p.scope, scope) && !places.some((x) => scopeKey(x.scope) === scopeKey(p.scope)));
}
