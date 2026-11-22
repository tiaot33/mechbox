// 同一层的别处同名、内容不同的几份：内容同不同要问技能库，这里是那次查询;行上怎么说见 row-state.ts。
import { useQuery } from "@tanstack/react-query";
import type { UnmanagedSkill } from "@bindings/internal/library";
import { sameNameCopiesQuery, useSnapshot } from "@/lib/library";
import { sameNameCopies, sameNameScopes } from "@/lib/same-name";
import { placeNames } from "@/lib/places";
import type { ScopeKey } from "@/lib/scope";

// SameNameCopies 是一个作用域里同名不同内容的几份(见 lib/same-name 的 sameNameCopies):copies 按这里的文件夹给出别处的地名(去过重);
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
  const copies = new Map([...found].map(([folder, others]): [string, string[]] => [folder, [...new Set(placeNames(s, others))]]));
  return { pending: query.isPending, copies };
}
