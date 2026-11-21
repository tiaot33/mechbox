// 更新的公共流程(issue #131):对一个集合生成更新计划(PlanUpdate),按勾选一次完成更新和重新安装(Update)。
// 更新计划弹窗、更新列表里的"更新"(按默认勾选直接执行)、"全部更新"、本地目录来源的"重新读取"
// 与用目录里的修改更新(PlanUpdateFromScope,issue #185)都走这里;完成后的汇报见 report.tsx。
// 默认勾哪些技能、哪些地方见 lib/consent.ts。
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ReinstallOutcome,
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
import { currentSnapshot } from "@/lib/library";
import { collectionName, isLocal } from "@/lib/model";
import { ui } from "@/lib/ui";
import { confirmLinkWrites } from "@/components/app/link-notes";
import { ScopeUpdateDialog } from "./scope-update-dialog";
import { UpdateDialog } from "./update-dialog";
import { UpdateHistory } from "./update-history";
import { reportUpdates } from "./report";
import {
  currentRecord,
  directoryReread,
  failedOnly,
  movedSince,
  refreshOutcome,
  rereadChange,
  splitByRecord,
  stalePlan,
  updateOutcome,
  type UpdateOutcome,
} from "./update-rules";

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
  const picked = updateInstalls(plan, checked, installOn);
  const { places, written, requests } = picked;
  if (confirmLinks && !(await confirmLinkWrites(places, written, messages().update.confirmTitle(label), messages().update.update))) return undefined;
  let conflict = false;
  const result = await ui.run(() => LibraryService.Update({ plan: plan.id, skills: [...checked], installs: requests }), {
    label: from ? messages().update.fromScopeLabel(from, label) : isLocal(plan.source) ? messages().update.rereadLabel(label) : messages().update.updateLabel([label]),
    onError: (err) => (conflict = stalePlan(err)),
    failed: (r) => (r.installs ?? []).some((x) => x.outcome === ReinstallOutcome.ReinstallFailed),
  });
  if (conflict) return "conflict";
  if (!result) return undefined;
  return updateOutcome(s, plan, label, checked, picked, result, from);
}

// placeAgain 把这几处换成当前版本:record 不为 0 时结果算进那次更新(回滚时一起装回去),否则是普通的重新安装。
// 返回与 requests 一一对应的结果;调用失败(含取消)时返回 undefined。
export function placeAgain(record: UpdateRecordID, requests: ReinstallRequest[], track?: (call: CancellablePromise<ReinstallResult[] | null>) => void) {
  return ui.run(() => {
    const call = record ? LibraryService.RetryUpdateInstalls(record, requests) : LibraryService.Reinstall(requests);
    track?.(call);
    return call;
  }, { failed: (r) => failedOnly(r).length > 0, label: messages().update.retryLabel });
}

// updateWithDefaults 不打开更新计划，按默认勾选更新这些集合，最后一起汇报。改名、删除和会换掉用户修改的更新要在更新计划里由用户回答，这里不动。
// 读来源、更新、换装出去的地方算一件事：整个过程顶栏的状态圈一直转、写着在更新哪几个，中间不画对勾;
// 一个都没更新(都没有能直接更新的，或在确认里取消了)时不画对勾，有的集合没更新成时写没有全部完成。
export async function updateWithDefaults(collectionIDs: CollectionID[]) {
  const s = currentSnapshot();
  const t = messages().update;
  const names = collectionIDs.map((id) => collectionName(s, id));
  const r = await ui.run(
    async () => {
      const outcomes: UpdateOutcome[] = [];
      let missed = 0;
      for (const [i, id] of collectionIDs.entries()) {
        const name = names[i];
        const plan = await ui.run(() => LibraryService.PlanUpdate(id), { reload: false });
        if (!plan) {
          missed++;
          continue;
        }
        const checked = defaultSkills(plan);
        if (!checked.size) {
          ui.toast(t.nothingDirect(name));
          continue;
        }
        const o = await runUpdate(plan, name, checked, defaultInstallOn, undefined, true);
        if (o === "conflict") ui.toast(t.sourceMoved(name), "error");
        if (o && o !== "conflict") outcomes.push(o);
        else missed++;
      }
      return { outcomes, missed };
    },
    { reload: false, label: t.updateLabel(names), failed: (r) => r.outcomes.length > 0 && r.missed > 0, canceled: (r) => !r.outcomes.length },
  );
  if (r) await reportUpdates(r.outcomes);
}

// rereadSkill 是本地目录来源的"重新读取":读出目录当前内容成为新版本，并换到装过它的地方(用户改过的、文件夹不在了的地方不动)。
// 底层就是只勾这一个技能的更新，所以照样有版本历史和回滚。读目录到换完算一件事，顶栏的状态圈中间不画对勾;没读成时不画对勾。
export async function rereadSkill(skill: Skill) {
  const o = await ui.run(() => reread(skill), { reload: false, label: messages().update.rereadLabel(skill.name), canceled: (o) => !o });
  if (o) await reportUpdates([o]);
}

async function reread(skill: Skill): Promise<UpdateOutcome | undefined> {
  const plan = await ui.run(() => LibraryService.PlanUpdate(skill.collection_id));
  if (!plan) return undefined;
  const change = rereadChange(plan, skill.id);
  if (change === "gone") {
    ui.toast(messages().update.goneFromDirectory(skill.name), "error");
    return undefined;
  }
  if (change === "unchanged") return refreshInstalls(skill);
  const o = await runUpdate(plan, skill.name, new Set([skill.id]), defaultInstallOn, undefined, true);
  if (o === "conflict") ui.toast(messages().update.directoryMoved, "error");
  return o === "conflict" ? undefined : o;
}

// rereadDirectory 是本地目录来源整个目录的"重新读取":目录里改过的技能都读出当前内容成为新版本，并换到装过它们的地方
// (用户改过的、文件夹不在了的地方不动);和逐个技能的重新读取一样是一次更新，有版本历史、可以回滚。
// 默认读进来的照更新计划的默认勾选：回滚掉的(跳过)、库里有用户修改的不读。还有要用户拿主意的(目录里删掉或改了名、
// 会换掉库里用户修改的)时打开更新计划，由用户在那里勾选、确认改名;目录和库里完全一样时只提示一句。读目录到换完算一件事，顶栏的状态圈中间不画对勾;没读进来或转去更新计划时不画对勾。
export async function rereadDirectory(collectionID: CollectionID) {
  const label = collectionName(currentSnapshot(), collectionID);
  const o = await ui.run(() => readDirectory(collectionID, label), {
    reload: false,
    label: messages().update.rereadLabel(label),
    canceled: (o) => !o || o === "decide",
  });
  if (o === "decide") return openUpdate(collectionID);
  if (o) await reportUpdates([o]);
}

async function readDirectory(collectionID: CollectionID, label: string): Promise<UpdateOutcome | "decide" | undefined> {
  const t = messages().update;
  const plan = await ui.run(() => LibraryService.PlanUpdate(collectionID));
  if (!plan) return undefined;
  const next = directoryReread(plan);
  if (next.kind === "decide") return "decide";
  if (next.kind === "nothing") {
    ui.toast(next.changed ? t.directoryNothingDefault(label) : t.directoryUnchanged(label));
    return undefined;
  }
  const o = await runUpdate(plan, label, next.checked, defaultInstallOn, undefined, true);
  if (o === "conflict") ui.toast(t.directoryMoved, "error");
  return o === "conflict" ? undefined : o;
}

// refreshInstalls 是目录内容没变时的"重新读取":库里已是目录现在的内容，但装出去的地方可能还没换上(上次没换上、
// 落后于库里的版本),把这些换成当前版本;用户改过的、文件夹不在了的地方不动，项目不在这台机器上的装不上。都已是当前版本时只提示一句。
// 带来当前版本的那次更新里列过的地方，补装算进那次更新，回滚它时一起装回去;其余的是普通的重新安装。
async function refreshInstalls(skill: Skill): Promise<UpdateOutcome | undefined> {
  const s = currentSnapshot();
  const inspected = await ui.read(() => LibraryService.InspectSkillScopes({ skill_id: skill.id }));
  if (inspected === undefined) return undefined;
  const kept = refreshRequests(inspected ?? []);
  const { requests, keptEdited, keptMissing } = kept;
  if (!requests.length) {
    ui.toast(messages().update.unchanged(skill.name, keptEdited, keptMissing));
    return undefined;
  }
  const wanted = new Set(requests.map(placeKey));
  if (!(await confirmLinkWrites(inspected ?? [], (p) => wanted.has(placeKey(p)), messages().update.rereadTitle(skill.name), messages().update.reinstall))) return undefined;
  const history = await ui.read(() => LibraryService.ListUpdateHistory({ skill_id: skill.id }));
  if (history === undefined) return undefined;
  const record = currentRecord(history ?? [], skill);
  const { viaRecord, plain } = splitByRecord(requests, record);
  const results: ReinstallResult[] = [];
  if (viaRecord.length) {
    const r = await placeAgain(record!.id, viaRecord);
    if (!r) return undefined;
    // 那次更新之后技能又换过版本(更新后又回滚)的地方，那次更新已管不到它们，按普通重新安装换上。
    const since = movedSince(viaRecord, r);
    results.push(...since.results);
    plain.push(...since.moved);
  }
  if (plain.length) {
    const r = await placeAgain(0, plain);
    if (!r) return undefined;
    results.push(...r);
  }
  return refreshOutcome(s, skill, record, kept, results, plain);
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
