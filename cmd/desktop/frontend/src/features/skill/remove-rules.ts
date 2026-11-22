// 移除技能(remove.tsx)里的判断：一批移除会涉及几个作用域，移除同名的另一个时能在哪几处换上留下的那个、哪些技能会因此找不到它提到的技能。
// 不碰界面、不调技能库，单元测试见 remove-rules.test.ts。
import type { MissingMention, Scope, SkillID } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { canInstall, skillScopeIn, skillScopesOf } from "@/lib/installs";
import { globalScopesSeenFrom, mergedScopeKey, placeCount } from "@/lib/places";
import { scopeKey } from "@/lib/scope";

// removedPlaces 是移除 ids 这几个技能时它们装着的各处，以及涉及几个作用域：几个技能装在同一个作用域只算一个，
// 项目里共用一个目录的几个 Agent 是一个。
export function removedPlaces(s: Snapshot, ids: SkillID[]) {
  const places = s.skillScopes.filter((p) => ids.includes(p.skill_id));
  return { places, scopes: placeCount(s, places.map((p) => p.scope)) };
}

// lostAfterTwin 是移除同名的另一个技能(name)、删掉它的文件夹之后会找不到被提到技能的地方:replaced 是在那几处装上留下的这个，
// 那几处提到这个名字的技能又找得到了(找到的是留下的这个),不再算。不换装时 replaced 为空，unfound 照旧。
export function lostAfterTwin(s: Snapshot, unfound: MissingMention[], name: string, replaced: Scope[]): MissingMention[] {
  const swapped = new Set(replaced.map((scope) => mergedScopeKey(s, scope)));
  return unfound.filter((m) => m.name !== name || !swapped.has(mergedScopeKey(s, m.scope)));
}

// twinReplaceScopes 是移除同名技能 id 时，能在它装着的几处换上留下的 keep 的那几处：项目不在这台机器上的装不上;
// 项目里那一处的 Agent 在全局装着 keep 的也不算：Agent 在项目里本来就用得到全局的那个，再装一份反而又多出一处同名。
// keep 不会和 id 装在同一处(同名就是同一个文件夹)。
export function twinReplaceScopes(s: Snapshot, id: SkillID, keep: SkillID): Scope[] {
  return skillScopesOf(s, id)
    .filter((p) => canInstall(p) && !globalScopesSeenFrom(s, scopeKey(p.scope)).some((key) => skillScopeIn(s, keep, key)))
    .map((p) => p.scope);
}
