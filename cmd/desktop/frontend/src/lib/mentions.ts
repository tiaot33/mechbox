// 技能之间的提到关系在安装、卸载和作用域页上怎样用：只整理技能库给出的结果(ListMissingMentions、ListLostMentions),不碰界面。
// 项目里共用一个目录的几个 Agent 在界面上是一处(见 model.ts 的 mergedScopeKey),各自的结果合在这一处。
import type { MissingMention, SkillInScope } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { placeKey } from "./consent";
import { mergedScopeKey } from "./model";

type AgentList = Pick<Snapshot, "agents" | "agentsShown">;

const at = (s: AgentList, p: SkillInScope) => `${p.skill_id}|${mergedScopeKey(s, p.scope)}`;

// missingAt 是装在这几处的技能提到的、在那里找不到的技能;共用目录的几个 Agent 的结果都算这一处的。
export function missingAt(s: AgentList, missing: MissingMention[], places: SkillInScope[]): MissingMention[] {
  const here = new Set(places.map((p) => at(s, p)));
  return missing.filter((m) => here.has(at(s, m)));
}

// mergeMissing 把共用目录的几个 Agent 里同一个技能找不到同一个名字的几条合成一条(留第一条),列出来时不重复。
export function mergeMissing(s: AgentList, missing: MissingMention[]): MissingMention[] {
  const seen = new Set<string>();
  return missing.filter((m) => {
    const key = `${at(s, m)}|${m.name}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

// installable 是能从库里装过去补上的：库里有这个名字的技能，装到提到它的技能所在的作用域;同一处只装一次。
export function installable(missing: MissingMention[]): SkillInScope[] {
  const seen = new Set<string>();
  const result: SkillInScope[] = [];
  for (const m of missing) {
    if (!m.mentioned_skill_id) continue;
    const target = { skill_id: m.mentioned_skill_id, scope: m.scope };
    if (seen.has(placeKey(target))) continue;
    seen.add(placeKey(target));
    result.push(target);
  }
  return result;
}

// missingNames 是几条里的技能名，按出现的先后、不重复。
export function missingNames(missing: MissingMention[]): string[] {
  return [...new Set(missing.map((m) => m.name))];
}
