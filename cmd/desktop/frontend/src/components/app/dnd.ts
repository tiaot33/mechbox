// 拖放：左 = 整理进哪个组，右 = 安装到哪个作用域。
// - 技能行 → 右栏作用域 = 直接安装;技能行 → 左栏的组或组的节标题 = 加入该组。
// - 组(左栏组行或组的节标题)→ 右栏作用域 = 组安装，之后加进组的也会装;→ 另一个组 = 调整组的先后。
// - 集合(左栏集合行或集合的节标题)→ 右栏作用域 = 把集合里的技能逐个直接安装，一次性的，之后入库的新技能不会自动装过去;
//   → 组 = 把集合里的技能逐个加入该组;→ 另一个集合 = 调整集合的先后。
// 调整先后时按鼠标在目标的上半还是下半，放到它前面或后面，左栏与列表的分节一起变(见 lib/view 的 reorder)。
// 正在拖的东西(DragItem)记在界面状态(lib/view)里;拖动时跟着鼠标的标签是 App 里的一个元素，经 setDragGhost 登记。
import type { CollectionID, GroupID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot, type Snapshot } from "@/lib/library";
import { parseScope, scopeKey, skillScopeIn, type ScopeKey } from "@/lib/model";
import { endDrag, reorder, reveal, startDrag, useView, type DragItem } from "@/lib/view";
import { addCollectionToGroup, addSkillToGroup } from "@/features/group/group";
import { installCollection, installGroup, installSkill } from "@/features/install/install";

let ghost: HTMLElement | null = null;

// setDragGhost 登记拖动标签的元素(作为 ref 回调)。
export function setDragGhost(el: HTMLElement | null) {
  ghost = el;
}

// useDnD 给拖动源与放置目标用：正在拖的东西、开始 / 结束拖动，放到作用域、组上，以及调整集合与组的先后。
export function useDnD() {
  const item = useView((v) => v.drag);
  return {
    item,
    start: (e: React.DragEvent, dragged: DragItem, label: string) => {
      e.dataTransfer.effectAllowed = "copyMove";
      e.dataTransfer.setData("text/plain", `${dragged.kind}:${dragged.id}`);
      if (ghost) {
        ghost.textContent = label;
        e.dataTransfer.setDragImage(ghost, 12, 12);
      }
      // 下一帧再改状态，避免重渲染打断浏览器的拖动开始。
      window.setTimeout(() => startDrag(dragged));
    },
    end: endDrag,
    dropOnScope: (key: ScopeKey) => {
      if (!item) return;
      const sc = parseScope(key);
      endDrag();
      // 拖动中临时展开的地方拖完会收回，放下的那一行保持展开，好看到装上的结果。
      reveal(sc);
      if (item.kind === "skill") void installSkill(item.id, sc);
      else if (item.kind === "group") void installGroup(item.id, sc);
      else void installCollection(item.id, sc);
    },
    dropOnGroup: (id: GroupID) => {
      if (item?.kind === "skill") {
        endDrag();
        void addSkillToGroup(id, item.id);
      } else if (item?.kind === "collection") {
        endDrag();
        void addCollectionToGroup(id, item.id);
      }
    },
    // dropOrder 把拖动的集合或组放到同类的 target 前面或后面。
    dropOrder: (target: { kind: "collection"; id: CollectionID } | { kind: "group"; id: GroupID }, after: boolean) => {
      if (item?.kind !== target.kind || item.id === target.id) return;
      endDrag();
      reorder(target.kind, item.id, target.id, after);
    },
  };
}

// alreadyInScope:拖动的东西已经在那个作用域(技能已直接安装 / 组已组安装 / 集合里的技能都已直接安装),放下没有意义。
export function alreadyInScope(s: Snapshot, item: DragItem, key: ScopeKey): boolean {
  if (item.kind === "skill") return !!skillScopeIn(s, item.id, key)?.direct;
  if (item.kind === "collection") {
    const ids = s.collections.find((c) => c.id === item.id)?.skills ?? [];
    return ids.length > 0 && ids.every((id) => skillScopeIn(s, id, key)?.direct);
  }
  return s.groupInstalls.some((a) => a.group_id === item.id && scopeKey(a.scope) === key);
}

// alreadyInGroup:技能已是该组成员，或集合里的技能都已是该组成员。
export function alreadyInGroup(s: Snapshot, item: DragItem, id: GroupID): boolean {
  const members = s.groups.find((g) => g.id === id)?.members ?? [];
  if (item.kind === "skill") return members.includes(item.id);
  if (item.kind === "collection") {
    const ids = s.collections.find((c) => c.id === item.id)?.skills ?? [];
    return ids.length > 0 && ids.every((x) => members.includes(x));
  }
  return false;
}

// canAddToGroup:拖动的是技能或集合，放到组上就是加入该组。
export const canAddToGroup = (item: DragItem | null): boolean => item?.kind === "skill" || item?.kind === "collection";

// dropProps 生成放置目标的事件处理:canDrop 为 false 时不接受放置。
export function dropProps(canDrop: boolean, onDrop: () => void) {
  return {
    onDragOver: (e: React.DragEvent) => {
      if (!canDrop) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
      (e.currentTarget as HTMLElement).dataset.dropHover = "";
    },
    onDragLeave: (e: React.DragEvent) => delete (e.currentTarget as HTMLElement).dataset.dropHover,
    onDrop: (e: React.DragEvent) => {
      delete (e.currentTarget as HTMLElement).dataset.dropHover;
      if (!canDrop) return;
      e.preventDefault();
      onDrop();
    },
  };
}

// orderProps 生成调整先后的放置目标的事件处理：悬停时按鼠标在上半还是下半标出放到前面还是后面(data-drop-order),
// canDrop 为 false 时不接受放置。
export function orderProps(canDrop: boolean, onDrop: (after: boolean) => void) {
  const after = (e: React.DragEvent) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    return e.clientY > r.top + r.height / 2;
  };
  return {
    onDragOver: (e: React.DragEvent) => {
      if (!canDrop) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      (e.currentTarget as HTMLElement).dataset.dropOrder = after(e) ? "after" : "before";
    },
    onDragLeave: (e: React.DragEvent) => delete (e.currentTarget as HTMLElement).dataset.dropOrder,
    onDrop: (e: React.DragEvent) => {
      delete (e.currentTarget as HTMLElement).dataset.dropOrder;
      if (!canDrop) return;
      e.preventDefault();
      onDrop(after(e));
    },
  };
}

// useCollectionDrop 让集合(左栏集合行、集合的节标题)接受拖来的另一个集合(调整先后)。
export function useCollectionDrop(id: CollectionID) {
  const dnd = useDnD();
  return orderProps(dnd.item?.kind === "collection" && dnd.item.id !== id, (after) => dnd.dropOrder({ kind: "collection", id }, after));
}

// useGroupDrop 让组(左栏组行、组的节标题)接受拖来的技能或集合(加入该组)与另一个组(调整先后)。
// 技能、集合已经都在组里时标出"已在"(data-already),不接受放置。
export function useGroupDrop(id: GroupID) {
  const s = useSnapshot();
  const dnd = useDnD();
  const t = useT();
  if (dnd.item?.kind === "group") return orderProps(dnd.item.id !== id, (after) => dnd.dropOrder({ kind: "group", id }, after));
  const already = !!dnd.item && alreadyInGroup(s, dnd.item, id);
  return {
    "data-already": already ? t.layout.alreadyThere : undefined,
    "data-drop-group": id,
    ...dropProps(canAddToGroup(dnd.item) && !already, () => dnd.dropOnGroup(id)),
  };
}
