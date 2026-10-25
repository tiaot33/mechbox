// 同名技能的标记与说明：在这个 Agent 里同名的几个技能会怎样(哪个生效、是否都出现)。
import { useT } from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";

// SameNameBadge 与 SameNameLine 标出作用域里和同一个 Agent 另一层级同名的技能：标签之外直接写出后果，
// 用户排查"这个项目里用的是不是我装的那个"时不用再点开。
export function SameNameBadge({ note }: { note: { title: string } }) {
  const t = useT().list;
  return (
    <Badge tone="warn" className="same-name-badge" title={note.title}>
      {t.sameName}
    </Badge>
  );
}

export function SameNameLine({ note }: { note: { text: string; title: string } }) {
  return (
    <p className="same-name-note truncate text-[10.5px] text-warn-strong" title={`${note.text}\n${note.title}`}>
      {note.text}
    </p>
  );
}
