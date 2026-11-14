// 集合与组的先后：左栏或列表里把集合、组拖到同类的前后，或在左栏节标题的右键菜单里恢复默认顺序。
// 先后存在技能库里(随推送、拉回走);拖放时先在快照里换好顺序，界面不等技能库就跟着变，写完再重读。
import { LibraryService } from "@bindings/cmd/desktop";
import { queryClient, reloadLibrary, snapshotQuery, type Snapshot } from "@/lib/library";
import { legacyOrder, moved, type LegacyOrder } from "@/lib/model";
import { ui } from "@/lib/ui";

type Kind = "collection" | "group";

// reorder 把拖动的集合或组(dragged)挪到同类的 target 前面或后面(after)。
export async function reorder(kind: Kind, dragged: number, target: number, after: boolean) {
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  if (!s) return;
  const inOrder = <T extends { id: number }>(items: T[], ids: number[]) => ids.map((id) => items.find((x) => x.id === id)!);
  const ids = moved((kind === "collection" ? s.collections : s.groups).map((x) => x.id), dragged, target, after);
  queryClient.setQueryData(
    snapshotQuery.queryKey,
    kind === "collection" ? { ...s, collections: inOrder(s.collections, ids) } : { ...s, groups: inOrder(s.groups, ids) },
  );
  await save(kind, ids);
}

// resetOrder 把集合恢复成首次入库的先后，或把组恢复成新建的先后。
export const resetOrder = (kind: Kind) => save(kind, []);

function save(kind: Kind, ids: number[]) {
  return ui.run(() => (kind === "collection" ? LibraryService.ReorderCollections(ids) : LibraryService.ReorderGroups(ids)), { busyMark: false });
}

// 旧版把拖出来的先后存在 localStorage 的这个键里。
const legacyKey = "mechbox.order";
let adopting = false;

// adoptLegacyOrder 在升级后第一次读到快照时，把旧版拖出来的先后写进技能库(规则见 lib/model 的 legacyOrder),写成了再删掉旧的那份;
// 写失败时留着，下次启动再试。没有旧的那份时什么都不做。
export async function adoptLegacyOrder(s: Snapshot) {
  const raw = localStorage.getItem(legacyKey);
  if (raw === null || adopting) return;
  adopting = true;
  try {
    const order = legacyOrder(s, parseLegacy(raw));
    if (order.collections) await LibraryService.ReorderCollections(order.collections);
    if (order.groups) await LibraryService.ReorderGroups(order.groups);
    localStorage.removeItem(legacyKey);
    if (order.collections || order.groups) void reloadLibrary();
  } catch {
    // 留着旧的那份，下次启动再试。
  } finally {
    adopting = false;
  }
}

function parseLegacy(raw: string): LegacyOrder {
  const ids = (x: unknown) => (Array.isArray(x) ? x.filter((id): id is number => typeof id === "number") : []);
  try {
    const v = JSON.parse(raw);
    return { collections: ids(v?.collections), groups: ids(v?.groups) };
  } catch {
    return { collections: [], groups: [] };
  }
}
