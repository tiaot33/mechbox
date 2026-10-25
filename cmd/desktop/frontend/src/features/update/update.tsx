// 更新的公共流程(issue #131):对一个集合生成更新计划(PlanUpdate),按勾选一次完成更新和重新安装(Update)。
// 更新计划弹窗、更新列表里的"更新"(按默认勾选直接执行)、"全部更新"、本地目录来源的"重新读取"
// 与用目录里的修改更新(PlanUpdateFromScope,issue #185)都走这里;完成后的汇报见 report.tsx。
// 默认勾哪些技能、哪些地方见 lib/consent.ts。
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  PlannedChange,
  ReinstallOutcome,
  UpdateRecordKind,
  type CollectionID,
  type ReinstallRequest,
  type ReinstallResult,
  type Scope,
  type Skill,
  type SkillID,
  type SkillScopeInspection,
  type UpdateHistoryFilter,
  type UpdatePlan,
  type UpdateRecordID,
} from "@bindings/internal/library";
import { defaultInstallOn, defaultSkills, placeKey, refreshRequests, updateInstalls } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot, type LibraryError } from "@/lib/library";
import { collectionName, isLocal, mergeProjectPlaces } from "@/lib/model";
import { ui } from "@/lib/ui";
import { confirmLinkWrites } from "@/components/app/link-notes";
import { ScopeUpdateDialog } from "./scope-update-dialog";
import { UpdateDialog } from "./update-dialog";
import { UpdateHistory } from "./update-history";
import { reportUpdates } from "./report";

export interface UpdateOutcome {
  label: string;
  local: boolean;
  from?: string; // 用目录里的修改更新时取内容的那一处，如"Claude Code · 全局"
  record: UpdateRecordID; // 这次更新的记录;库里没有新版本时为 0,没有可回滚的
  changed: number; // 换了新版本的技能数
  placed: number; // 换成新版的地方
  keptEdited: number; // 用户改过、没勾，保持原样的地方
  keptMissing: number; // 文件夹不在了、没勾，没装回去的地方
  failed: ReinstallResult[];
  requests: ReinstallRequest[];
  plain: Set<string>; // 不算进 record 的地方(placeKey),没换上时按普通重新安装重试
}

// stalePlan 是更新计划用不了了：生成之后来源或库变了(Conflict),句柄已失效，或勾选的技能在来源里没了、在库里被移除了(NotFound)。
// 这时让用户重新生成更新计划，而不是报错。
export function stalePlan(err: LibraryError): boolean {
  return err.code === ErrorCode.Conflict || err.code === ErrorCode.NotFound;
}

// runUpdate 按更新计划执行一次更新：库先换(全成或全不成),勾选的地方再逐处换。
// 计划生成后来源或库变了时返回 "conflict",由调用方让用户重新看计划;其他失败已提示错误，返回 undefined。
export async function runUpdate(
  plan: UpdatePlan,
  label: string,
  checked: Set<SkillID>,
  installOn: (p: SkillScopeInspection) => boolean,
  from?: string,
  confirmLinks = false, // 不经更新计划弹窗的快捷更新：写到符号链接相关的地方前先确认
): Promise<UpdateOutcome | "conflict" | undefined> {
  const s = currentSnapshot();
  const { places, written, requests, keptEdited, keptMissing } = updateInstalls(plan, checked, installOn);
  if (confirmLinks && !(await confirmLinkWrites(places, written, messages().update.confirmTitle(label), messages().update.update))) return undefined;
  let conflict = false;
  const result = await ui.run(() => LibraryService.Update({ plan: plan.id, skills: [...checked], installs: requests }), {
    onError: (err) => (conflict = stalePlan(err)),
    failed: (r) => (r.installs ?? []).some((x) => x.outcome === ReinstallOutcome.ReinstallFailed),
  });
  if (conflict) return "conflict";
  if (!result) return undefined;
  const installs = result.installs ?? [];
  return {
    label,
    local: isLocal(plan.source),
    from,
    record: result.record ?? 0,
    changed: (result.skills ?? []).filter((x) => x.changed).length,
    placed: mergeProjectPlaces(s, installs.filter((r) => r.outcome !== ReinstallOutcome.ReinstallFailed)).length,
    keptEdited,
    keptMissing,
    failed: installs.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed),
    requests,
    plain: new Set(),
  };
}

// placeAgain 把这几处换成当前版本:record 不为 0 时结果算进那次更新(回滚时一起装回去),否则是普通的重新安装。
// 返回与 requests 一一对应的结果;调用失败(含取消)时返回 undefined。
export function placeAgain(record: UpdateRecordID, requests: ReinstallRequest[], track?: (call: CancellablePromise<ReinstallResult[] | null>) => void) {
  return ui.run(() => {
    const call = record ? LibraryService.RetryUpdateInstalls(record, requests) : LibraryService.Reinstall(requests);
    track?.(call);
    return call;
  }, { failed: (r) => failedOnly(r).length > 0 });
}

export const failedOnly = (results: ReinstallResult[] | null) => (results ?? []).filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed);

// updateWithDefaults 不打开更新计划，按默认勾选更新这些集合，最后一起汇报。改名、删除和会换掉用户修改的更新要在更新计划里由用户回答，这里不动。
export async function updateWithDefaults(collectionIDs: CollectionID[]) {
  const s = currentSnapshot();
  const outcomes: UpdateOutcome[] = [];
  const t = messages().update;
  for (const id of collectionIDs) {
    const name = collectionName(s, id);
    const plan = await ui.run(() => LibraryService.PlanUpdate(id), { reload: false });
    if (!plan) continue;
    const checked = defaultSkills(plan);
    if (!checked.size) {
      ui.toast(t.nothingDirect(name));
      continue;
    }
    const o = await runUpdate(plan, name, checked, defaultInstallOn, undefined, true);
    if (o === "conflict") ui.toast(t.sourceMoved(name), "error");
    else if (o) outcomes.push(o);
  }
  await reportUpdates(outcomes);
}

// rereadSkill 是本地目录来源的"重新读取":读出目录当前内容成为新版本，并换到装过它的地方(用户改过的、文件夹不在了的地方不动)。
// 底层就是只勾这一个技能的更新，所以照样有版本历史和回滚。
export async function rereadSkill(skill: Skill) {
  const plan = await ui.run(() => LibraryService.PlanUpdate(skill.collection_id));
  if (!plan) return;
  const item = plan.skills?.find((p) => p.skill.id === skill.id);
  if (!item || item.change === PlannedChange.PlannedDeleted) {
    ui.toast(messages().update.goneFromDirectory(skill.name), "error");
    return;
  }
  if (item.change === PlannedChange.PlannedUnchanged) {
    const o = await refreshInstalls(skill);
    if (o) await reportUpdates([o]);
    return;
  }
  const o = await runUpdate(plan, skill.name, new Set([skill.id]), defaultInstallOn, undefined, true);
  if (o === "conflict") ui.toast(messages().update.directoryMoved, "error");
  else if (o) await reportUpdates([o]);
}

// refreshInstalls 是目录内容没变时的"重新读取":库里已是目录现在的内容，但装出去的地方可能还没换上(上次没换上、
// 落后于库里的版本),把这些换成当前版本;用户改过的、文件夹不在了的地方不动，项目不在这台机器上的装不上。都已是当前版本时只提示一句。
// 带来当前版本的那次更新里列过的地方，补装算进那次更新，回滚它时一起装回去;其余的是普通的重新安装。
async function refreshInstalls(skill: Skill): Promise<UpdateOutcome | undefined> {
  const s = currentSnapshot();
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes({ skill_id: skill.id }), { reload: false });
  if (inspected === undefined) return undefined;
  const { requests, keptEdited, keptMissing } = refreshRequests(inspected ?? []);
  if (!requests.length) {
    ui.toast(messages().update.unchanged(skill.name, keptEdited, keptMissing));
    return undefined;
  }
  const wanted = new Set(requests.map(placeKey));
  if (!(await confirmLinkWrites(inspected ?? [], (p) => wanted.has(placeKey(p)), messages().update.rereadTitle(skill.name), messages().update.reinstall))) return undefined;
  const history = await ui.run(() => LibraryService.ListUpdateHistory({ skill_id: skill.id }), { reload: false });
  if (history === undefined) return undefined;
  const record = (history ?? []).find(
    (r) =>
      r.kind === UpdateRecordKind.UpdateRecordUpdate &&
      !r.rolled_back_by &&
      (r.skills ?? []).some((x) => x.skill_id === skill.id && x.to_version_id === skill.version_id && x.from_version_id !== x.to_version_id),
  );
  const listed = new Set((record?.installs ?? []).map(placeKey));
  const viaRecord = requests.filter((r) => listed.has(placeKey(r)));
  const plain = requests.filter((r) => !listed.has(placeKey(r)));
  const results: ReinstallResult[] = [];
  if (viaRecord.length) {
    const r = await placeAgain(record!.id, viaRecord);
    if (!r) return undefined;
    // 那次更新之后技能又换过版本(更新后又回滚)的地方，那次更新已管不到它们，按普通重新安装换上。
    const moved = viaRecord.filter((_, i) => r[i]?.error?.code === ErrorCode.Conflict);
    results.push(...r.filter((_, i) => r[i]?.error?.code !== ErrorCode.Conflict));
    plain.push(...moved);
  }
  if (plain.length) {
    const r = await placeAgain(0, plain);
    if (!r) return undefined;
    results.push(...r);
  }
  return {
    label: skill.name,
    local: true,
    record: record?.id ?? 0,
    changed: 0,
    placed: mergeProjectPlaces(s, results.filter((r) => r.outcome !== ReinstallOutcome.ReinstallFailed)).length,
    keptEdited,
    keptMissing,
    failed: failedOnly(results),
    requests,
    plain: new Set(plain.map(placeKey)),
  };
}

// updateFromScope 打开"用这里的修改更新":把巡检为被手动改过的那一处的内容作为技能的新版本，并换到勾选的其他地方。
export function updateFromScope(skill: Skill, scope: Scope) {
  return ui.open((close) => <ScopeUpdateDialog skill={skill} scope={scope} onClose={() => close()} />);
}

// openUpdate 打开集合的更新计划。从技能发起时传 focus:默认只勾这一个，其余折叠;
// 从跳过记录找回时传 select:默认勾上那几个。
export function openUpdate(collectionID: CollectionID, opts: { focus?: SkillID; select?: SkillID[] } = {}) {
  const s = currentSnapshot();
  if (!s.collections.some((c) => c.id === collectionID)) return;
  return ui.open((close) => <UpdateDialog collectionID={collectionID} focus={opts.focus} select={opts.select} onClose={() => close()} />);
}


// openHistory 打开集合的历史(按集合)或某个技能的历史(按技能:哪几次更新改过它)。
export function openHistory(filter: UpdateHistoryFilter, title: string) {
  return ui.open((close) => <UpdateHistory filter={filter} title={title} onClose={() => close()} />);
}
