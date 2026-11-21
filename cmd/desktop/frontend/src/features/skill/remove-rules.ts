// 移除技能(remove.tsx)里的判断：一批移除会涉及几个作用域，移除同名的另一个时哪些技能会因此找不到它提到的技能。
// 不碰界面、不调技能库，单元测试见 remove-rules.test.ts。
import type { MissingMention, Scope, SkillID } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { mergedScopeKey } from "@/lib/model";

// removedPlaces 是移除 ids 这几个技能时它们装着的各处，以及涉及几个作用域：几个技能装在同一个作用域只算一个，
// 项目里共用一个目录的几个 Agent 是一个。
export function removedPlaces(s: Snapshot, ids: SkillID[]) {
  const places = s.skillScopes.filter((p) => ids.includes(p.skill_id));
  return { places, scopes: new Set(places.map((p) => mergedScopeKey(s, p.scope))).size };
}

// lostAfterTwin 是移除同名的另一个技能(name)、删掉它的文件夹之后会找不到被提到技能的地方:replaced 是在那几处装上留下的这个，
// 那几处提到这个名字的技能又找得到了(找到的是留下的这个),不再算。不换装时 replaced 为空，unfound 照旧。
export function lostAfterTwin(s: Snapshot, unfound: MissingMention[], name: string, replaced: Scope[]): MissingMention[] {
  const swapped = new Set(replaced.map((scope) => mergedScopeKey(s, scope)));
  return unfound.filter((m) => m.name !== name || !swapped.has(mergedScopeKey(s, m.scope)));
}
