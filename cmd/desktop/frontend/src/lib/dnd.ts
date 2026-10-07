// 拖放(原型变体 A):左 = 整理进哪个组，右 = 安装到哪个作用域。
// - 技能卡片 → 右栏作用域 = 直接安装;技能卡片 → 左栏组 = 加入该组。
// - 组(左栏组行或列表顶部的组标题条)→ 右栏作用域 = 组安装。
import { createContext, useContext } from "react";
import type { GroupID, SkillID } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { scopeKey, skillScopeIn, type ScopeKey } from "./model";

export type DragItem = { kind: "skill"; id: SkillID } | { kind: "group"; id: GroupID };

export interface DnD {
  item: DragItem | null;
  start: (e: React.DragEvent, item: DragItem, label: string) => void;
  end: () => void;
  dropOnScope: (key: ScopeKey) => void;
  dropOnGroup: (id: GroupID) => void;
}

export const DnDContext = createContext<DnD | null>(null);

export function useDnD(): DnD {
  const dnd = useContext(DnDContext);
  if (!dnd) throw new Error("useDnD outside DnDContext");
  return dnd;
}

// alreadyInScope:拖动的东西已经在那个作用域(技能已直接安装 / 组已组安装),放下没有意义。
export function alreadyInScope(s: Snapshot, item: DragItem, key: ScopeKey): boolean {
  if (item.kind === "skill") return !!skillScopeIn(s, item.id, key)?.direct;
  return s.groupInstalls.some((a) => a.group_id === item.id && scopeKey(a.scope) === key);
}

// alreadyInGroup:技能已是该组成员。
export function alreadyInGroup(s: Snapshot, item: DragItem, id: GroupID): boolean {
  return item.kind === "skill" && !!s.groups.find((g) => g.group.id === id)?.members?.includes(item.id);
}

// dropProps 生成放置目标的事件处理:canDrop 为 false 时不接受放置。
export function dropProps(canDrop: boolean, onDrop: () => void) {
  return {
    onDragOver: (e: React.DragEvent) => {
      if (!canDrop) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
      (e.currentTarget as HTMLElement).classList.add("drop-hover");
    },
    onDragLeave: (e: React.DragEvent) => (e.currentTarget as HTMLElement).classList.remove("drop-hover"),
    onDrop: (e: React.DragEvent) => {
      (e.currentTarget as HTMLElement).classList.remove("drop-hover");
      if (!canDrop) return;
      e.preventDefault();
      onDrop();
    },
  };
}
