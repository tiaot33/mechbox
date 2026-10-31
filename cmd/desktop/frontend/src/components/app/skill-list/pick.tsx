// 勾选：行与节标题上的勾选框。平时鼠标移上去才出现，勾了至少一个以后一直显示;按住 Shift 点，勾上或取消从上一次点的那一行到这一行。
import { useEffect, useRef } from "react";
import type { SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { setPicked, togglePick, useView } from "@/lib/view";

const boxClass = "size-3.5 shrink-0 cursor-pointer accent-primary transition-opacity";

// listOrder 是列表里各行从上到下的技能，按住 Shift 勾选一段时用。
const listOrder = () =>
  [...document.querySelectorAll<HTMLElement>(".a-list .skill-row[data-skill-id]")].map((el) => Number(el.dataset.skillId));

// PickBox 是一行开头的勾选框。
export function PickBox({ id }: { id: SkillID }) {
  const picked = useView((v) => v.picked.has(id));
  const any = useView((v) => v.picked.size > 0);
  const t = useT().list;
  return (
    <span className="flex" onClick={(e) => e.stopPropagation()}>
      <input
        type="checkbox"
        className={cn("pick-box", boxClass, !any && "opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100")}
        checked={picked}
        aria-label={t.pick}
        onChange={() => {}}
        onClick={(e) => togglePick(id, e.shiftKey ? { order: listOrder() } : undefined)}
      />
    </span>
  );
}

// SectionPick 是节标题上的勾选框：勾上或取消这一节列出的全部技能(折起来的"没装的"也算);勾了一部分时是半选。
export function SectionPick({ ids }: { ids: SkillID[] }) {
  const count = useView((v) => ids.filter((id) => v.picked.has(id)).length);
  const any = useView((v) => v.picked.size > 0);
  const ref = useRef<HTMLInputElement>(null);
  const t = useT().list;
  const all = ids.length > 0 && count === ids.length;
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = count > 0 && !all;
  }, [count, all]);
  if (!ids.length) return null;
  return (
    <input
      ref={ref}
      type="checkbox"
      className={cn("section-pick", boxClass, !any && "opacity-0 group-hover/head:opacity-100 focus-visible:opacity-100")}
      checked={all}
      aria-label={t.pickSection}
      title={t.pickSection}
      onChange={() => setPicked(ids, !all)}
    />
  );
}
