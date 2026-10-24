// 拖放(原型变体 A):左 = 整理进哪个组，右 = 安装到哪个作用域。
// - 技能卡片 → 右栏作用域 = 直接安装;技能卡片 → 左栏组 = 加入该组。
// - 组(左栏组行或列表顶部的组标题条)→ 右栏作用域 = 组安装。
// 正在拖的东西记在界面状态(lib/view)里;拖动时跟着鼠标的标签是 App 里的一个元素，经 setDragGhost 登记。
import type { GroupID, SkillID } from "@bindings/internal/library";
import { addSkillToGroup, installGroup, installSkill } from "./actions";
import { useSnapshot, type Snapshot } from "./library";
import { parseScope, scopeKey, skillScopeIn, type ScopeKey } from "./model";
import { useUI } from "./ui";
import { endDrag, reveal, startDrag, useView } from "./view";

export type DragItem = { kind: "skill"; id: SkillID } | { kind: "group"; id: GroupID };

let ghost: HTMLElement | null = null;

// setDragGhost 登记拖动标签的元素(作为 ref 回调)。
export function setDragGhost(el: HTMLElement | null) {
  ghost = el;
}

// useDnD 给拖动源与放置目标用：正在拖的东西、开始 / 结束拖动，以及放到作用域、组上。
export function useDnD() {
  const ui = useUI();
  const s = useSnapshot();
  const item = useView((v) => v.drag);
  return {
    item,
    start: (e: React.DragEvent, dragged: DragItem, label: string) => {
      e.dataTransfer.effectAllowed = "copy";
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
      void (item.kind === "skill" ? installSkill(ui, s, item.id, sc) : installGroup(ui, s, item.id, sc));
    },
    dropOnGroup: (id: GroupID) => {
      if (item?.kind !== "skill") return;
      endDrag();
      void addSkillToGroup(ui, s, id, item.id);
    },
  };
}

// alreadyInScope:拖动的东西已经在那个作用域(技能已直接安装 / 组已组安装),放下没有意义。
export function alreadyInScope(s: Snapshot, item: DragItem, key: ScopeKey): boolean {
  if (item.kind === "skill") return !!skillScopeIn(s, item.id, key)?.direct;
  return s.groupInstalls.some((a) => a.group_id === item.id && scopeKey(a.scope) === key);
}

// alreadyInGroup:技能已是该组成员。
export function alreadyInGroup(s: Snapshot, item: DragItem, id: GroupID): boolean {
  return item.kind === "skill" && !!s.groups.find((g) => g.id === id)?.members?.includes(item.id);
}

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
