// 更新的公共流程(issue #131):对一个集合发起，一次确认同时完成更新和重新安装。
// 更新确认框、更新列表里的"更新"(按默认勾选直接执行)与"全部更新"都走这里。
// 默认勾选：有新版本的技能全勾(从技能发起时只勾这一个);装出去的地方全勾，用户改过的地方不勾，项目不在这台机器上的不能勾。
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ReinstallOutcome,
  SkillScopeStatus,
  UpdateCheckStatus,
  type Collection,
  type ReinstallRequest,
  type ReinstallResult,
  type Skill,
  type SkillID,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { describeError } from "./errors";
import type { Snapshot } from "./library";
import { collectionSkills, deletedInSource, isLocal, scopeKey, scopeLabel, sourceLabel, updateAvailable, type ScopeKey } from "./model";
import { Modal, type useUI } from "./ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";

type UI = ReturnType<typeof useUI>;

// CollectionUpdate 是一个集合在更新时的样子：哪些技能有新版本、哪些没变化、哪些来源里已删除。
export interface CollectionUpdate {
  collection: Collection;
  local: boolean;
  label: string; // 集合名;集合里只有一个技能时就是技能名，不出现集合这一层
  skills: Skill[];
  available: Skill[];
  unchanged: Skill[]; // 已是最新：一起带进更新，内容不变，只是确认到来源的同一个来源版本上，集合因此对齐
  deleted: Skill[];
}

export function collectionUpdate(s: Snapshot, collection: Collection, focus?: SkillID): CollectionUpdate {
  const local = isLocal(collection.source);
  const skills = collectionSkills(s, collection);
  return {
    collection,
    local,
    // 集合里只有一个技能、或本地目录来源从技能发起时，说的就是这个技能。
    label:
      skills.length === 1
        ? skills[0].name
        : local && focus
          ? (skills.find((x) => x.id === focus)?.name ?? sourceLabel(s, collection.source))
          : sourceLabel(s, collection.source),
    skills,
    // 本地目录来源不检查更新，只重新读取发起的那个技能。
    available: local ? skills.filter((x) => x.id === focus) : skills.filter(updateAvailable),
    unchanged: local ? [] : skills.filter((x) => x.last_update_check?.status === UpdateCheckStatus.UpdateCurrent),
    deleted: local ? [] : skills.filter(deletedInSource),
  };
}

// ScopeRow 是"装在这些地方"里的一行：一个作用域里装着的、这次勾选的技能。
export interface ScopeRow {
  key: ScopeKey;
  places: SkillScopeInspection[];
  modified: SkillScopeInspection[];
  rootMissing: boolean;
}

export function scopeRows(inspections: SkillScopeInspection[], checked: Set<SkillID>): ScopeRow[] {
  const rows: ScopeRow[] = [];
  for (const p of inspections) {
    if (!checked.has(p.skill_id)) continue;
    const key = scopeKey(p.scope);
    let row = rows.find((r) => r.key === key);
    if (!row) rows.push((row = { key, places: [], modified: [], rootMissing: false }));
    row.places.push(p);
    if (p.status === SkillScopeStatus.SkillScopeModified) row.modified.push(p);
    if (p.status === SkillScopeStatus.SkillScopeRootMissing) row.rootMissing = true;
  }
  return rows;
}

export const defaultScopeOn = (r: ScopeRow) => !r.rootMissing && r.modified.length === 0;

export interface UpdateOutcome {
  label: string;
  local: boolean;
  changed: number; // 有新版本写进库的技能数
  placed: number; // 换成当前版本的地方
  keptEdited: number; // 用户改过、没勾，保持原样的地方
  failed: ReinstallResult[];
}

// runUpdate 执行一次更新：库先换(全成或全不成),装出去的地方再逐处换。更新失败时已提示错误，返回 undefined。
export async function runUpdate(
  ui: UI,
  u: CollectionUpdate,
  checked: Set<SkillID>,
  rows: ScopeRow[],
  scopeOn: (r: ScopeRow) => boolean,
): Promise<UpdateOutcome | undefined> {
  const ids = [...checked, ...u.unchanged.map((x) => x.id)];
  const requests: ReinstallRequest[] = rows.filter(scopeOn).flatMap((r) =>
    r.places
      .filter((p) => p.status !== SkillScopeStatus.SkillScopeRootMissing)
      .map((p) => ({ skill_id: p.skill_id, scope: p.scope, overwrite: p.status === SkillScopeStatus.SkillScopeModified })),
  );
  // 没有要换的地方时，更新完就重读快照;否则等重新安装完再重读。
  const result = await ui.run(() => LibraryService.Update({ collection_id: u.collection.id, skills: ids }), {
    reload: !requests.length,
  });
  if (!result) return undefined;
  const reinstalled = requests.length ? ((await ui.run(() => LibraryService.Reinstall(requests))) ?? []) : [];
  return {
    label: u.label,
    local: u.local,
    changed: (result.skills ?? []).filter((x) => x.changed && checked.has(x.skill.id)).length,
    placed: reinstalled.filter((r) => r.outcome !== ReinstallOutcome.ReinstallFailed).length,
    keptEdited: rows.filter((r) => !scopeOn(r)).reduce((n, r) => n + r.modified.length, 0),
    failed: reinstalled.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed),
  };
}

function summarize(o: UpdateOutcome): string {
  return [
    o.local ? `已重新读取「${o.label}」` : `已更新 ${o.label}`,
    o.changed ? `${o.changed} 个技能有新版本` : "内容没有变化",
    o.placed ? `换到了 ${o.placed} 个地方` : "",
    o.keptEdited ? `${o.keptEdited} 个地方你改过，没动` : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

// reportUpdates 汇报一次或几次更新：都换上了用 toast;有地方没换上时列出原因。
export async function reportUpdates(ui: UI, s: Snapshot, outcomes: UpdateOutcome[]) {
  if (!outcomes.length) return;
  const failed = outcomes.flatMap((o) => o.failed);
  const lines = outcomes.map(summarize);
  if (!failed.length) {
    ui.toast(lines.join("\n"));
    return;
  }
  const name = (id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;
  await ui.open((close) => (
    <Modal
      title="有地方没换上"
      onClose={() => close()}
      footer={
        <Button variant="primary" onClick={() => close()}>
          知道了
        </Button>
      }
    >
      <Notice className="whitespace-pre-line">
        {lines.join("\n")}
        {"\n"}下面这些地方没换上，保持原样，可以在那里单独重新安装。
      </Notice>
      <ListBox>
        {failed.map((r, i) => (
          <ListRow key={i}>
            <div className="min-w-0 grow">
              <div>
                {name(r.skill_id)} · {scopeLabel(s, r.scope)}
              </div>
              {r.error && <div className="text-xs whitespace-pre-line text-muted-foreground">{describeError(r.error)}</div>}
            </div>
            <Badge tone="bad">没换上</Badge>
          </ListRow>
        ))}
      </ListBox>
    </Modal>
  ));
}

// updateWithDefaults 不打开更新确认框，按默认勾选更新这些集合，最后一起汇报。
export async function updateWithDefaults(ui: UI, s: Snapshot, collections: Collection[]) {
  const inspections = await ui.run(() => LibraryService.InspectSkillScopes({}), { reload: false });
  if (inspections === undefined) return;
  const outcomes: UpdateOutcome[] = [];
  for (const c of collections) {
    const u = collectionUpdate(s, c);
    const checked = new Set(u.available.map((x) => x.id));
    if (!checked.size) continue;
    const o = await runUpdate(ui, u, checked, scopeRows(inspections ?? [], checked), defaultScopeOn);
    if (o) outcomes.push(o);
  }
  await reportUpdates(ui, s, outcomes);
}
