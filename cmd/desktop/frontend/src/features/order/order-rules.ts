// 集合与组的先后(order.ts)里的判断：拖到同类的前后之后是什么先后，把旧版存在 localStorage 里的先后换成交给技能库的完整先后。
// 不碰界面、不调技能库，单元测试见 order-rules.test.ts。
import type { CollectionID, GroupID } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { isDefaultOrder, sourceLabel } from "@/lib/source";
import { collator } from "@/lib/utils";

// moved 是把 dragged 挪到 target 的前面(after 为 false)或后面之后的先后;target 不在 ids 里时不变。
export function moved(ids: number[], dragged: number, target: number, after: boolean): number[] {
  if (dragged === target) return ids;
  const rest = ids.filter((id) => id !== dragged);
  const at = rest.indexOf(target);
  if (at < 0) return ids;
  rest.splice(after ? at + 1 : at, 0, dragged);
  return rest;
}

// LegacyOrder 是旧版存在 localStorage 里、用户拖出来的先后(集合与组的 ID)。
export interface LegacyOrder {
  collections: CollectionID[];
  groups: GroupID[];
}

// legacyOrder 把旧版拖出来的先后换成要交给技能库的完整先后，照旧版的规则：拖过的在前，其余集合按来源名、组按 ID 接在后面。
// 某一类没拖过(或拖过的都已不在库里)、或库里已经不是默认顺序(升级后又拖过)时，那一类不用写，为 undefined。
export function legacyOrder(s: Snapshot, saved: LegacyOrder): { collections?: CollectionID[]; groups?: GroupID[] } {
  const merged = (dragged: number[], rest: number[]) => {
    const kept = dragged.filter((id) => rest.includes(id));
    return kept.length ? [...kept, ...rest.filter((id) => !kept.includes(id))] : undefined;
  };
  const collections = s.collections.map((c) => c.id);
  const groups = s.groups.map((g) => g.id);
  const byName = [...s.collections].sort((a, b) => collator.compare(sourceLabel(s, a.source), sourceLabel(s, b.source))).map((c) => c.id);
  return {
    collections: isDefaultOrder(collections) ? merged(saved.collections, byName) : undefined,
    groups: isDefaultOrder(groups) ? merged(saved.groups, groups) : undefined,
  };
}
