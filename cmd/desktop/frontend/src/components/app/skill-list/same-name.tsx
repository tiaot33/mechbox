// 同名技能的标记与说明：在这个 Agent 里同名的几个技能会怎样(哪个生效、是否都出现)。
import { useT } from "@/lib/i18n";
import { AlertDot, type Alert } from "@/components/ui/badge";

type Note = { text: string; title: string };

// sameNameAlert 标出作用域里和同一个 Agent 另一层级同名的技能：行里只画「注意」一级的图标，鼠标停住时写出后果
// (哪个生效、是否都出现),用户排查"这个项目里用的是不是我装的那个"时不用再点开。
export function sameNameAlert(note: Note, label: string): Alert {
  return { tone: "warn", label, tip: sameNameTip(note), className: "same-name-badge" };
}

export function SameNameBadge({ note }: { note: Note }) {
  const t = useT().list;
  return <AlertDot items={[sameNameAlert(note, t.sameName)]} />;
}

// sameNameTip 是同名提醒的说明：先写后果，再写是哪个文件夹;两句一样时(技能库页)只写一遍。
function sameNameTip(note: Note): string {
  return note.title === note.text ? note.text : `${note.text}\n${note.title}`;
}
