// 同名技能的标记与说明：在这个 Agent 里同名的几个技能会怎样(哪个生效、是否都出现)。
import { useT } from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";

// SameNameBadge 标出作用域里和同一个 Agent 另一层级同名的技能：行里只画「注意」一级的圆点，鼠标停住时写出后果
// (哪个生效、是否都出现),用户排查"这个项目里用的是不是我装的那个"时不用再点开。
// SameNameLine 是同一句后果，留在 DOM 里给读屏，界面上不占一行(行高固定)。
export function SameNameBadge({ note }: { note: { text: string; title: string } }) {
  const t = useT().list;
  return (
    <Badge tone="warn" compact className="same-name-badge" tip={sameNameTip(note)}>
      {t.sameName}
    </Badge>
  );
}

// sameNameTip 是同名提醒的说明：先写后果，再写是哪个文件夹;两句一样时(技能库页)只写一遍。
export function sameNameTip(note: { text: string; title: string }): string {
  return note.title === note.text ? note.text : `${note.text}\n${note.title}`;
}

export function SameNameLine({ note }: { note: { text: string; title: string } }) {
  return <p className="same-name-note sr-only">{note.text}</p>;
}
