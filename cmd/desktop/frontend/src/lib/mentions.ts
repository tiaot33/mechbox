// 技能之间的提到关系在安装、卸载和技能详情里怎样用：只整理技能库给出的结果(ListMissingMentions、GetSkillMentions),不碰界面。
import type { MissingMention, Scope, SkillID } from "@bindings/internal/library";
import { scopeKey } from "./model";

// Placed 是装在某个作用域的一个技能(安装、卸载的那一处)。
export interface Placed {
  skill_id: SkillID;
  scope: Scope;
}

const placedKey = (p: Placed) => `${p.skill_id}|${scopeKey(p.scope)}`;
const missingKey = (m: MissingMention) => `${placedKey(m)}|${m.name}`;

// missingAt 是装在这几处的技能提到的、在那里找不到的技能。
export function missingAt(missing: MissingMention[], places: Placed[]): MissingMention[] {
  const at = new Set(places.map(placedKey));
  return missing.filter((m) => at.has(placedKey(m)));
}

// newlyMissing 是 after 比 before 多出来的：卸载、删除之前，当那几处已经不在时才会找不到的。
export function newlyMissing(before: MissingMention[], after: MissingMention[]): MissingMention[] {
  const had = new Set(before.map(missingKey));
  return after.filter((m) => !had.has(missingKey(m)));
}

// installable 是能从库里装过去补上的：库里有这个名字的技能，装到提到它的技能所在的作用域;同一处只装一次。
export function installable(missing: MissingMention[]): Placed[] {
  const seen = new Set<string>();
  const result: Placed[] = [];
  for (const m of missing) {
    if (!m.mentioned_skill_id) continue;
    const target = { skill_id: m.mentioned_skill_id, scope: m.scope };
    if (seen.has(placedKey(target))) continue;
    seen.add(placedKey(target));
    result.push(target);
  }
  return result;
}

// names 是几条里的技能名，按出现的先后、不重复。
export function missingNames(missing: MissingMention[]): string[] {
  return [...new Set(missing.map((m) => m.name))];
}
