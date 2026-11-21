// 入库预览(fetch-review.tsx)里的判断：分组标题上的勾选，勾上的候选提到了哪些没勾的候选。默认勾哪些见 lib/consent.ts 的 defaultCandidates。
// 不碰界面、不调技能库，单元测试见 fetch-review-rules.test.ts。
import type { Candidate } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { sameNameInLibrary } from "@/lib/model";

// toggleCandidates 是分组标题上的勾选(members 是这一组的候选):组里没入库的候选都勾上时取消全组，否则全组勾上;已入库的不能选。
export function toggleCandidates(selected: ReadonlySet<string>, members: Candidate[]): Set<string> {
  const names = members.filter((c) => !c.fetched).map((c) => c.name);
  const next = new Set(selected);
  const all = names.every((n) => selected.has(n));
  for (const n of names) {
    if (all) next.delete(n);
    else next.add(n);
  }
  return next;
}

// unpickedMentions 是勾上的候选 c 提到了、同一份预览里没勾也还没入库的候选的名字(行里写出它们，点一下一起勾上;技能库只按 SKILL.md、按规则认出)。
// 库里已有同名技能的候选默认不勾、要用户看过提醒自己决定，这里也不算：库里那个就能满足这次提到。s 还没读到时不认库里的同名技能。
export function unpickedMentions(s: Snapshot | undefined, candidates: Candidate[], selected: ReadonlySet<string>, c: Candidate): string[] {
  if (!selected.has(c.name)) return [];
  return (c.mentions ?? []).filter((name) => {
    const other = candidates.find((x) => !x.fetched && x.name === name);
    return !selected.has(name) && !!other && (!s || sameNameInLibrary(s, { id: 0, name: other.name }).length === 0);
  });
}
