// 关系图要画什么：集合或组里的技能、它们提到的库里其他集合的技能，以及谁提到谁;没有连线的技能不进图，收在图下方。
// 只是把技能库给出的「提到」关系整理成节点与连线，不碰界面;布局在 components/app/mention-graph 里做。
import type { Mention, Skill, SkillID } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { collator, sourceLabel } from "./model";

// GraphNode 是图上的一个技能。outside 的是本集合或组以外的技能(用虚线框画出),where 写它在哪个集合;没有来源的写 null。
export interface GraphNode {
  skill: Skill;
  outside: boolean;
  where?: string | null;
}

// MentionGraph 是一张关系图:nodes 按名字排,edges 是技能库给出的提到关系(两头都在 nodes 里),
// loose 是本集合或组里和谁都没有连线的技能，按名字排。
export interface MentionGraph {
  nodes: GraphNode[];
  edges: Mention[];
  loose: Skill[];
}

export function mentionGraph(s: Snapshot, members: SkillID[], mentions: Mention[]): MentionGraph {
  const byID = new Map(s.skills.map((x) => [x.id, x]));
  const inSet = new Set(members);
  const edges = mentions.filter((m) => m.from !== m.to && inSet.has(m.from) && byID.has(m.from) && byID.has(m.to));
  const linked = new Set(edges.flatMap((m) => [m.from, m.to]));
  const byName = (a: Skill, b: Skill) => collator.compare(a.name, b.name) || a.id - b.id;
  const nodes = [...linked]
    .map((id) => byID.get(id)!)
    .sort(byName)
    .map((skill): GraphNode => {
      if (inSet.has(skill.id)) return { skill, outside: false };
      const c = s.collections.find((x) => x.id === skill.collection_id);
      return { skill, outside: true, where: c ? sourceLabel(s, c.source) : null };
    });
  const loose = members
    .map((id) => byID.get(id))
    .filter((x): x is Skill => !!x && !linked.has(x.id))
    .sort(byName);
  return { nodes, edges, loose };
}

// neighbours 是悬停一个技能时要高亮的：它自己、它提到的和提到它的技能，以及相连的线(按 edgeKey)。
export function neighbours(edges: Mention[], id: SkillID): { nodes: Set<SkillID>; edges: Set<string> } {
  const nodes = new Set<SkillID>([id]);
  const lines = new Set<string>();
  for (const m of edges) {
    if (m.from !== id && m.to !== id) continue;
    nodes.add(m.from);
    nodes.add(m.to);
    lines.add(edgeKey(m));
  }
  return { nodes, edges: lines };
}

export const edgeKey = (m: Pick<Mention, "from" | "to">) => `${m.from}->${m.to}`;
