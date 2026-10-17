// 更新的公共流程(issue #131):对一个集合生成更新计划(PlanUpdate),按勾选一次完成更新和重新安装(Update)。
// 更新计划弹窗、更新列表里的"更新"(按默认勾选直接执行)、"全部更新"与本地目录来源的"重新读取"都走这里;
// 完成后的提示带回滚入口，没换上的地方可以单独重试。
// 默认勾选：有变化且没跳过的技能全勾(从技能发起时只勾这一个);推测出的改名要用户点"是";
// 装出去的地方全勾，用户改过的地方不勾，项目不在这台机器上的不能勾。
import { useEffect, useRef, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  PlannedChange,
  ReinstallOutcome,
  RollbackOutcome,
  SkillScopeStatus,
  UpdateRecordKind,
  UpdateRiskKind,
  type CollectionID,
  type PlannedSkill,
  type ReinstallRequest,
  type ReinstallResult,
  type RollbackInstall,
  type Skill,
  type SkillID,
  type SkillScopeInspection,
  type UpdateHistoryFilter,
  type UpdatePlan,
  type UpdateRecordID,
  type UpdateRisk,
} from "@bindings/internal/library";
import { describeError } from "./errors";
import type { LibraryError, Snapshot } from "./library";
import { collectionSkills, isLocal, scopeKey, scopeLabel, sourceLabel } from "./model";
import { Modal, type useUI } from "./ui";
import { FetchReview } from "@/components/app/fetch-review";
import { UpdateDialog } from "@/components/app/update-dialog";
import { UpdateHistory } from "@/components/app/update-history";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";

type UI = ReturnType<typeof useUI>;

export const skillName = (s: Snapshot, id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;

// collectionName 是集合在界面上的名字：集合里只有一个技能时就是技能名，不出现集合这一层。
export function collectionName(s: Snapshot, collectionID: CollectionID): string {
  const c = s.collections.find((x) => x.id === collectionID);
  if (!c) return `集合 #${collectionID}`;
  const skills = collectionSkills(s, c);
  return skills.length === 1 ? skills[0].name : sourceLabel(s, c.source);
}

// ---------- 勾选 ----------

// movable 是更新计划里能勾上更新的技能：有变化，或推测出了改名(勾上即确认"是")。
export const movable = (p: PlannedSkill) => p.change === PlannedChange.PlannedChanged || !!p.renamed_to;

// defaultSkills 是默认勾选：有变化且没跳过的全勾;给了 select(从技能发起、从跳过记录找回)时只勾这几个。
// 改名推测都不默认勾，等用户回答"是"。
export function defaultSkills(plan: UpdatePlan, select?: SkillID[]): Set<SkillID> {
  const skills = plan.skills ?? [];
  if (select) {
    return new Set(skills.filter((p) => select.includes(p.skill.id) && !p.renamed_to && p.change !== PlannedChange.PlannedDeleted).map((p) => p.skill.id));
  }
  return new Set(skills.filter((p) => p.change === PlannedChange.PlannedChanged && !p.skipped).map((p) => p.skill.id));
}

export const installKey = (p: { skill_id: SkillID; scope: SkillScopeInspection["scope"] }) => `${p.skill_id}|${scopeKey(p.scope)}`;
// folderKey 是装出去的实际文件夹:几个 Agent 共用一个目录时，它们的这一处是同一个文件夹，只能一起换。
export const folderKey = (p: SkillScopeInspection) => `${p.skill_id}|${p.folder}`;
export const canInstall = (p: SkillScopeInspection) => p.status !== SkillScopeStatus.SkillScopeRootMissing;
export const defaultInstallOn = (p: SkillScopeInspection) => canInstall(p) && p.status !== SkillScopeStatus.SkillScopeModified;

// plannedInstalls 是勾选的技能装出去的各处;更新计划只列有变化(或推测出改名)的技能。
export function plannedInstalls(plan: UpdatePlan, checked: Set<SkillID>): SkillScopeInspection[] {
  return (plan.installs ?? []).filter((p) => checked.has(p.skill_id));
}

// ---------- 风险 ----------

const riskText: Record<string, string> = {
  [UpdateRiskKind.RiskExecutableAdded]: "新增可执行文件",
  [UpdateRiskKind.RiskScriptAdded]: "新增脚本",
  [UpdateRiskKind.RiskScriptChanged]: "改动脚本",
};

export const riskLabel = (r: UpdateRisk) => riskText[r.kind] ?? "需要看一眼";

// riskSummary 把几处风险合成一句，如"新增 1 个脚本 · 改动 2 个脚本";没有风险时为空。
export function riskSummary(risks: UpdateRisk[]): string {
  const count = (kind: UpdateRiskKind) => risks.filter((r) => r.kind === kind).length;
  return [
    count(UpdateRiskKind.RiskExecutableAdded) && `新增 ${count(UpdateRiskKind.RiskExecutableAdded)} 个可执行文件`,
    count(UpdateRiskKind.RiskScriptAdded) && `新增 ${count(UpdateRiskKind.RiskScriptAdded)} 个脚本`,
    count(UpdateRiskKind.RiskScriptChanged) && `改动 ${count(UpdateRiskKind.RiskScriptChanged)} 个脚本`,
  ]
    .filter(Boolean)
    .join(" · ");
}

// ---------- 执行 ----------

export interface UpdateOutcome {
  label: string;
  local: boolean;
  record: UpdateRecordID; // 这次更新的记录;库里没有新版本时为 0,没有可回滚的
  changed: number; // 换了新版本的技能数
  placed: number; // 换成新版的地方
  keptEdited: number; // 用户改过、没勾，保持原样的地方
  failed: ReinstallResult[];
  requests: ReinstallRequest[];
  plain: Set<string>; // 不算进 record 的地方(installKey),没换上时按普通重新安装重试
}

// stalePlan 是更新计划用不了了：生成之后来源或库变了(Conflict),句柄已失效，或勾选的技能在来源里没了、在库里被移除了(NotFound)。
// 这时让用户重新生成更新计划，而不是报错。
export function stalePlan(err: LibraryError): boolean {
  return err.code === ErrorCode.Conflict || err.code === ErrorCode.NotFound;
}

// runUpdate 按更新计划执行一次更新：库先换(全成或全不成),勾选的地方再逐处换。
// 计划生成后来源或库变了时返回 "conflict",由调用方让用户重新看计划;其他失败已提示错误，返回 undefined。
export async function runUpdate(
  ui: UI,
  plan: UpdatePlan,
  label: string,
  checked: Set<SkillID>,
  installOn: (p: SkillScopeInspection) => boolean,
): Promise<UpdateOutcome | "conflict" | undefined> {
  const places = plannedInstalls(plan, checked);
  const requests: ReinstallRequest[] = places
    .filter((p) => canInstall(p) && installOn(p))
    .map((p) => ({ skill_id: p.skill_id, scope: p.scope, overwrite: p.status === SkillScopeStatus.SkillScopeModified }));
  let conflict = false;
  const result = await ui.run(() => LibraryService.Update({ plan: plan.id, skills: [...checked], installs: requests }), {
    onError: (err) => (conflict = stalePlan(err)),
  });
  if (conflict) return "conflict";
  if (!result) return undefined;
  const installs = result.installs ?? [];
  return {
    label,
    local: isLocal(plan.source),
    record: result.record ?? 0,
    changed: (result.skills ?? []).filter((x) => x.changed).length,
    placed: installs.filter((r) => r.outcome !== ReinstallOutcome.ReinstallFailed).length,
    keptEdited: new Set(places.filter((p) => p.status === SkillScopeStatus.SkillScopeModified && !installOn(p)).map(folderKey)).size,
    failed: installs.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed),
    requests,
    plain: new Set(),
  };
}

// placeAgain 把这几处换成当前版本:record 不为 0 时结果算进那次更新(回滚时一起装回去),否则是普通的重新安装。
// 返回与 requests 一一对应的结果;调用失败(含取消)时返回 undefined。
function placeAgain(ui: UI, record: UpdateRecordID, requests: ReinstallRequest[], track?: (call: CancellablePromise<ReinstallResult[] | null>) => void) {
  return ui.run(() => {
    const call = record ? LibraryService.RetryUpdateInstalls(record, requests) : LibraryService.Reinstall(requests);
    track?.(call);
    return call;
  });
}

const failedOnly = (results: ReinstallResult[] | null) => (results ?? []).filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed);

function summarize(o: UpdateOutcome): string {
  return [
    o.local ? `已重新读取「${o.label}」` : `已更新 ${o.label}`,
    o.changed ? `${o.changed} 个技能换了新版本` : "内容没有变化",
    o.placed ? `换到了 ${o.placed} 个地方` : "",
    o.keptEdited ? `${o.keptEdited} 个地方你改过，没动` : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

// reportUpdates 汇报一次或几次更新：都换上了用 toast,一次更新时带"回滚";有地方没换上时列出原因，可以单独重试。
export async function reportUpdates(ui: UI, s: Snapshot, outcomes: UpdateOutcome[]) {
  if (!outcomes.length) return;
  if (outcomes.every((o) => !o.failed.length)) {
    if (outcomes.length === 1) {
      const o = outcomes[0];
      ui.toast(summarize(o), "ok", o.record && o.changed ? { label: "回滚", onClick: () => void rollbackUpdate(ui, s, o.record) } : undefined);
    } else {
      ui.toast([...outcomes.map(summarize), "要退回某一次，可以在历史里回滚"].join("\n"));
    }
    return;
  }
  await ui.open((close) => <FailedPlaces ui={ui} s={s} outcomes={outcomes} close={() => close()} />);
}

// FailedPlaces 列出没换上的地方和原因;"重试"把它们单独再换一次，结果算进那次更新(回滚时一起装回去)。
// 重试中途关掉弹窗时取消正在换的这一次，后面的集合不再继续。
function FailedPlaces({ ui, s, outcomes, close }: { ui: UI; s: Snapshot; outcomes: UpdateOutcome[]; close: () => void }) {
  const [failed, setFailed] = useState(() => outcomes.map((o) => o.failed));
  const [working, setWorking] = useState(false);
  const alive = useRef(true);
  const call = useRef<CancellablePromise<ReinstallResult[] | null> | null>(null);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      void call.current?.cancel();
    };
  }, []);
  const left = failed.flat().length;
  const retry = async () => {
    setWorking(true);
    const next: ReinstallResult[][] = [];
    for (const [i, o] of outcomes.entries()) {
      const again = o.requests.filter((r) => failed[i].some((f) => installKey(f) === installKey(r)));
      const viaRecord = o.record ? again.filter((r) => !o.plain.has(installKey(r))) : [];
      const left: ReinstallResult[] = [];
      for (const [batch, record] of [
        [viaRecord, o.record],
        [again.filter((r) => !viaRecord.includes(r)), 0],
      ] as const) {
        if (!batch.length) continue;
        const results = await placeAgain(ui, record, batch, (c) => (call.current = c));
        call.current = null;
        if (!alive.current) return;
        left.push(...(results ? failedOnly(results) : failed[i].filter((f) => batch.some((r) => installKey(r) === installKey(f)))));
      }
      next.push(left);
    }
    setWorking(false);
    setFailed(next);
    if (!next.flat().length) {
      ui.toast("没换上的地方都换上了");
      close();
    }
  };
  // 只补装、没换版本的(目录没变时的重新读取)不提供回滚：这次没有可退回的改动。
  const single = outcomes.length === 1 && outcomes[0].record && outcomes[0].changed ? outcomes[0] : undefined;
  return (
    <Modal
      title="有地方没换上"
      onClose={close}
      footer={
        <>
          {single && (
            <Button
              onClick={() => {
                close();
                void rollbackUpdate(ui, s, single.record);
              }}
            >
              回滚
            </Button>
          )}
          <span className="grow" />
          <Button onClick={close}>知道了</Button>
          <Button variant="primary" loading={working} disabled={!left} onClick={retry}>
            重试没换上的 {left} 个地方
          </Button>
        </>
      }
    >
      <Notice className="whitespace-pre-line">
        {outcomes.map(summarize).join("\n")}
        {"\n"}下面这些地方没换上，保持原样;处理好原因后可以重试。
      </Notice>
      <ListBox>
        {failed.flat().map((r) => (
          <ListRow key={installKey(r)}>
            <div className="min-w-0 grow">
              <div>
                {skillName(s, r.skill_id)} · {scopeLabel(s, r.scope)}
              </div>
              {r.error && <div className="text-xs whitespace-pre-line text-muted-foreground">{describeError(r.error)}</div>}
            </div>
            <Badge tone="bad">没换上</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  );
}

// updateWithDefaults 不打开更新计划，按默认勾选更新这些集合，最后一起汇报。改名和删除要在更新计划里由用户回答，这里不动。
export async function updateWithDefaults(ui: UI, s: Snapshot, collectionIDs: CollectionID[]) {
  const outcomes: UpdateOutcome[] = [];
  for (const id of collectionIDs) {
    const name = collectionName(s, id);
    const plan = await ui.run(() => LibraryService.PlanUpdate(id), { reload: false });
    if (!plan) continue;
    const checked = defaultSkills(plan);
    if (!checked.size) {
      ui.toast(`${name} 没有能直接更新的改动;改名和删除请在更新计划里确认`);
      continue;
    }
    const o = await runUpdate(ui, plan, name, checked, defaultInstallOn);
    if (o === "conflict") ui.toast(`${name} 的来源刚又变了，请打开更新计划重新看`, "error");
    else if (o) outcomes.push(o);
  }
  await reportUpdates(ui, s, outcomes);
}

// rereadSkill 是本地目录来源的"重新读取":读出目录当前内容成为新版本，并换到装过它的地方(用户改过的地方不动)。
// 底层就是只勾这一个技能的更新，所以照样有版本历史和回滚。
export async function rereadSkill(ui: UI, s: Snapshot, skill: Skill) {
  const plan = await ui.run(() => LibraryService.PlanUpdate(skill.collection_id));
  if (!plan) return;
  const item = plan.skills?.find((p) => p.skill.id === skill.id);
  if (!item || item.change === PlannedChange.PlannedDeleted) {
    ui.toast(`目录里已经没有「${skill.name}」了`, "error");
    return;
  }
  if (item.change === PlannedChange.PlannedUnchanged) {
    const o = await refreshInstalls(ui, skill);
    if (o) await reportUpdates(ui, s, [o]);
    return;
  }
  const o = await runUpdate(ui, plan, skill.name, new Set([skill.id]), defaultInstallOn);
  if (o === "conflict") ui.toast("目录在读取期间又变了，请再重新读取一次", "error");
  else if (o) await reportUpdates(ui, s, [o]);
}

// refreshInstalls 是目录内容没变时的"重新读取":库里已是目录现在的内容，但装出去的地方可能还没换上(上次没换上、
// 文件夹不在了),把这些换成当前版本;用户改过的地方不动，项目不在这台机器上的装不上。都已是当前版本时只提示一句。
// 带来当前版本的那次更新里列过的地方，补装算进那次更新，回滚它时一起装回去;其余的是普通的重新安装。
async function refreshInstalls(ui: UI, skill: Skill): Promise<UpdateOutcome | undefined> {
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes({ skill_id: skill.id }), { reload: false });
  if (inspected === undefined) return undefined;
  const behind = (inspected ?? []).filter((p) => p.status !== SkillScopeStatus.SkillScopeCurrent && canInstall(p));
  const keptEdited = new Set(behind.filter((p) => p.status === SkillScopeStatus.SkillScopeModified).map(folderKey)).size;
  const requests: ReinstallRequest[] = behind
    .filter((p) => p.status !== SkillScopeStatus.SkillScopeModified)
    .map((p) => ({ skill_id: p.skill_id, scope: p.scope, overwrite: false }));
  if (!requests.length) {
    ui.toast([`「${skill.name}」的内容没有变化`, keptEdited ? `${keptEdited} 个地方你改过，没动` : ""].filter(Boolean).join(" · "));
    return undefined;
  }
  const history = await ui.run(() => LibraryService.ListUpdateHistory({ skill_id: skill.id }), { reload: false });
  if (history === undefined) return undefined;
  const record = (history ?? []).find(
    (r) =>
      r.kind === UpdateRecordKind.UpdateRecordUpdate &&
      !r.rolled_back_by &&
      (r.skills ?? []).some((x) => x.skill_id === skill.id && x.to_version_id === skill.version_id && x.from_version_id !== x.to_version_id),
  );
  const listed = new Set((record?.installs ?? []).map(installKey));
  const viaRecord = requests.filter((r) => listed.has(installKey(r)));
  const plain = requests.filter((r) => !listed.has(installKey(r)));
  const results: ReinstallResult[] = [];
  if (viaRecord.length) {
    const r = await placeAgain(ui, record!.id, viaRecord);
    if (!r) return undefined;
    // 那次更新之后技能又换过版本(更新后又回滚)的地方，那次更新已管不到它们，按普通重新安装换上。
    const moved = viaRecord.filter((_, i) => r[i]?.error?.code === ErrorCode.Conflict);
    results.push(...r.filter((_, i) => r[i]?.error?.code !== ErrorCode.Conflict));
    plain.push(...moved);
  }
  if (plain.length) {
    const r = await placeAgain(ui, 0, plain);
    if (!r) return undefined;
    results.push(...r);
  }
  return {
    label: skill.name,
    local: true,
    record: record?.id ?? 0,
    changed: 0,
    placed: results.filter((r) => r.outcome !== ReinstallOutcome.ReinstallFailed).length,
    keptEdited,
    failed: failedOnly(results),
    requests,
    plain: new Set(plain.map(installKey)),
  };
}

// openUpdate 打开集合的更新计划。从技能发起时传 focus:默认只勾这一个，其余折叠;
// 从跳过记录找回时传 select:默认勾上那几个。
export function openUpdate(ui: UI, s: Snapshot, collectionID: CollectionID, opts: { focus?: SkillID; select?: SkillID[] } = {}) {
  if (!s.collections.some((c) => c.id === collectionID)) return;
  return ui.open((close) => <UpdateDialog s={s} collectionID={collectionID} focus={opts.focus} select={opts.select} onClose={() => close()} />);
}

// ---------- 回滚 ----------

const rollbackText: Record<string, { text: string; tone: Tone }> = {
  [RollbackOutcome.RollbackReinstalled]: { text: "已装回", tone: "ok" },
  [RollbackOutcome.RollbackCurrent]: { text: "本来就是旧版", tone: "ok" },
  [RollbackOutcome.RollbackReinstalledSince]: { text: "之后又重新安装过，没动", tone: "warn" },
  [RollbackOutcome.RollbackModified]: { text: "你改过，没动", tone: "warn" },
  [RollbackOutcome.RollbackMissing]: { text: "文件夹不在了，没动", tone: "warn" },
  [RollbackOutcome.RollbackRootMissing]: { text: "项目不在这台机器上，没动", tone: "warn" },
  [RollbackOutcome.RollbackUninstalled]: { text: "已不装在这里", tone: "neutral" },
  [RollbackOutcome.RollbackRemoved]: { text: "技能已从库里移除", tone: "neutral" },
  [RollbackOutcome.RollbackFailed]: { text: "没装回", tone: "bad" },
};

export const rollbackOutcome = (outcome: string) => rollbackText[outcome] ?? { text: "没装回，需要查看", tone: "warn" as Tone };

const putBack = (r: RollbackInstall) => r.outcome === RollbackOutcome.RollbackReinstalled || r.outcome === RollbackOutcome.RollbackCurrent;

// rollbackUpdate 回滚一次更新：库换回那次更新之前的版本，那次装过的地方按磁盘内容核对后装回;没装回的列出原因。
export async function rollbackUpdate(ui: UI, s: Snapshot, record: UpdateRecordID) {
  const ok = await ui.confirm({
    title: "回滚这次更新",
    body: (
      <Hint>
        库里的技能换回这次更新之前的版本，这次换过的地方也装回旧版;之后又重新安装过、你改过或不在这台机器上的地方不动。
        回滚掉的改动记为跳过，不再计入角标;想要时可以在更新计划里再勾上。
      </Hint>
    ),
    confirm: "回滚",
  });
  if (!ok) return;
  const result = await ui.run(() => LibraryService.Rollback(record), {
    onError: (err) => {
      if (err.code !== ErrorCode.FailedPrecondition) return false;
      ui.toast("这次更新已经回滚过了", "error");
      return true;
    },
  });
  if (!result) return;
  const names = new Map<SkillID, string>([...(result.removed ?? []), ...(result.skills ?? [])].map((x) => [x.skill_id, x.name]));
  const name = (id: SkillID) => names.get(id) ?? skillName(s, id);
  const installs = result.installs ?? [];
  const back = installs.filter(putBack).length;
  const left = installs.filter((r) => !putBack(r));
  const removed = result.removed ?? [];
  const head = [
    `已回滚:${(result.skills ?? []).map((x) => x.name).join("、") || "没有可换回的技能"}换回了更新之前的版本`,
    back ? `装回了 ${back} 个地方` : "",
  ]
    .filter(Boolean)
    .join(" · ");
  if (!left.length && !removed.length) {
    ui.toast(head);
    return;
  }
  await ui.open((close) => (
    <Modal
      title="回滚完成"
      onClose={() => close()}
      footer={
        <Button variant="primary" onClick={() => close()}>
          知道了
        </Button>
      }
    >
      <Notice>{head}。下面这些没有装回，保持原样。</Notice>
      <ListBox>
        {removed.map((x) => (
          <ListRow key={`removed-${x.skill_id}`}>
            <span className="grow">{x.name}</span>
            <Badge>技能已从库里移除</Badge>
          </ListRow>
        ))}
        {left.map((r) => (
          <ListRow key={installKey(r)}>
            <div className="min-w-0 grow">
              <div>
                {name(r.skill_id)} · {scopeLabel(s, r.scope)}
              </div>
              {r.error && <div className="text-xs whitespace-pre-line text-muted-foreground">{describeError(r.error)}</div>}
            </div>
            <Badge tone={rollbackOutcome(r.outcome).tone}>{rollbackOutcome(r.outcome).text}</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  ));
}

// ---------- 历史、新技能与来源里已删除 ----------

// openHistory 打开集合的历史(按集合)或某个技能的历史(按技能:哪几次更新改过它)。
export function openHistory(ui: UI, s: Snapshot, filter: UpdateHistoryFilter, title: string) {
  return ui.open((close) => <UpdateHistory s={s} filter={filter} title={title} onClose={() => close()} />);
}

// previewNewSkills 打开这个集合来源的入库预览，新出现的技能默认勾上。
export async function previewNewSkills(ui: UI, collectionID: CollectionID) {
  const preview = await ui.run(() => LibraryService.PreviewFetch({ collection: collectionID }), { reload: false });
  if (!preview) return;
  await ui.open((close) => <FetchReview preview={preview} onlyNew onClose={() => close()} onBack={() => close()} />);
}

// notInterested 是对新技能点"不感兴趣":集合上不再提，入库预览里仍能找到。
export async function notInterested(ui: UI, collectionID: CollectionID, names: string[]): Promise<boolean> {
  const done = await ui.run(() => LibraryService.MarkNotInterested(collectionID, names).then(() => true), {
    success: "不再提示这几个新技能;想要时在「看来源里的其他技能」里仍能找到",
  });
  return !!done;
}

// keepDeleted 是对来源里已删除的技能选"继续保留":不再提醒，只在技能上留一个淡色标记。
export function keepDeleted(ui: UI, s: Snapshot, id: SkillID) {
  return ui.run(() => LibraryService.KeepDeletedSkill(id), { success: `已继续保留「${skillName(s, id)}」,不再提醒` });
}
