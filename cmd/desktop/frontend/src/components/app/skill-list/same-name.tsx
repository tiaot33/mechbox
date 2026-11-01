// 同名技能的标记：在这个 Agent 里同名的几个技能会怎样(哪个生效、是否都出现)。
import { useT } from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";

// SameNameBadge 标出作用域里和同一个 Agent 另一层级同名的技能。行只占一行，后果不另起一行写：
// 鼠标停在标签上，第一行写后果(用户排查"这个项目里用的是不是我装的那个"时看这一行)，下面列出另一处的文件夹。
export function SameNameBadge({ note }: { note: { text: string; title: string } }) {
  const t = useT().list;
  return (
    <Badge tone="warn" className="same-name-badge" title={`${note.text}\n${note.title}`}>
      {t.sameName}
    </Badge>
  );
}
