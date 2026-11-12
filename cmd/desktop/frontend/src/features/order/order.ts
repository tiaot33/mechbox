// 集合与组的先后：左栏或列表里把集合、组拖到同类的前后，或在左栏节标题的右键菜单里恢复默认顺序。
// 先后存在技能库里(随推送、拉回走);拖放时先在快照里换好顺序，界面不等技能库就跟着变，写完再重读。
import { LibraryService } from "@bindings/cmd/desktop";
import { queryClient, snapshotQuery } from "@/lib/library";
import { moved } from "@/lib/model";
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
