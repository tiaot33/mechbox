// 同名技能的标记与说明：在这个 Agent 里同名的几个技能会怎样(哪个生效、是否都出现)。
import { useQuery } from "@tanstack/react-query";
import type { UnmanagedSkill } from "@bindings/internal/library";
import { sameNameCopiesQuery, useSnapshot } from "@/lib/library";
import type { Messages } from "@/lib/i18n";
import { sameNameCopies, sameNameScopes, scopeLabel, type ScopeKey } from "@/lib/model";
import type { Alert } from "@/components/ui/badge";

type Note = { text: string; title: string };

// sameNameAlert 标出作用域里和同一个 Agent 另一层级同名的技能：行里只画「注意」一级的图标，鼠标停住时写出后果
// (哪个生效、是否都出现),用户排查"这个项目里用的是不是我装的那个"时不用再点开。
export function sameNameAlert(note: Note, label: string): Alert {
  return { tone: "warn", label, tip: sameNameTip(note), className: "same-name-badge" };
}

// sameNameTip 是同名提醒的说明：先写后果，再写是哪个文件夹;两句一样时(技能库页)只写一遍。
function sameNameTip(note: Note): string {
  return note.title === note.text ? note.text : `${note.text}\n${note.title}`;
}

// SameNameCopies 是一个作用域里同名不同内容的几份(见 model 的 sameNameCopies):copies 按这里的文件夹给出别处的地名(去过重);
// pending 是还在等技能库比较内容(这时整批入库的确认还给不出同名提醒，入口先等着)。
export interface SameNameCopies {
  pending: boolean;
  copies: Map<string, string[]>;
}

export function useSameNameCopies(scope: ScopeKey | undefined): SameNameCopies {
  const s = useSnapshot();
  const scopes = scope ? sameNameScopes(s, scope) : [];
  const query = useQuery(sameNameCopiesQuery(scopes));
  if (!scope || scopes.length < 2) return { pending: false, copies: new Map() };
  const found = query.data ? sameNameCopies(query.data, scope) : new Map<string, UnmanagedSkill[]>();
  const copies = new Map([...found].map(([folder, others]): [string, string[]] => [folder, [...new Set(others.map((x) => scopeLabel(s, x.scope)))]]));
  return { pending: query.isPending, copies };
}

// sameNameCopiesAlert 是行上「别处有同名的，内容不同」的提醒;places 是别处的地名。
export function sameNameCopiesAlert(t: Messages["list"], name: string, places: string[] | undefined): Alert | undefined {
  if (!places?.length) return undefined;
  return { tone: "warn", label: t.sameNameCopies, tip: t.sameNameCopiesTitle(name, places), className: "same-name-copies" };
}
