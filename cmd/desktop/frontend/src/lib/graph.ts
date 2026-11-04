// 关系图要画什么：集合或组里的技能、它们提到的本集合或组以外的技能，以及谁提到谁;没有连线的技能不进图，收在图下方。
// 只是把技能库给出的「提到」关系整理成节点与连线，不碰界面;布局在 components/app/mention-graph 里做。
import type { Mention, Skill, SkillID } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { collator, matches, sourceLabel } from "./model";

// GraphNode 是图上的一个技能。outside 的是本集合或组以外的技能(用虚线框画出),where 写它在哪个集合(没有来源的写「没有来源」)。
export interface GraphNode {
  skill: Skill;
  outside: boolean;
  where?: string;
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
    .map((skill): GraphNode => (inSet.has(skill.id) ? { skill, outside: false } : { skill, outside: true, where: sourceLabel(s, skill.source) }));
  const loose = members
    .map((id) => byID.get(id))
    .filter((x): x is Skill => !!x && !linked.has(x.id))
    .sort(byName);
  return { nodes, edges, loose };
}

// layoutKey 是决定布局的那部分：哪些技能(名字、是否在外面、在哪个集合)、哪些线。只有它变了才重新排;
// 快照别的地方变了(装了、写了备注、检查了更新)图不动。
export function layoutKey(graph: MentionGraph): string {
  return JSON.stringify([graph.nodes.map((n) => [n.skill.id, n.skill.name, n.outside, n.where ?? ""]), graph.edges.map(edgeKey)]);
}

// searchHits 是关系图页搜索的结果个数：图里的技能(包括虚线框里的)与图下方没有连线的技能，名字或描述匹配的。
export function searchHits(graph: MentionGraph, search: string): number {
  return graph.nodes.filter((n) => matches(search, n.skill)).length + graph.loose.filter((x) => matches(search, x)).length;
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

// Snippet 是提示里的一行原文：cut 为 true 时前面省掉了一段;parts 按顺序拼回原文，hit 的那段是被提到的名字。
export interface Snippet {
  cut: boolean;
  parts: { text: string; hit: boolean }[];
}

const nameChar = /[A-Za-z0-9_-]/;

// mentionSnippet 把一行原文切成片段，标出里面完整出现的名字(前后不是名字里的字符);
// 行很长、名字又在后面时，从名字前 lead 个字符开始，保证截成几行显示时名字看得到。
export function mentionSnippet(text: string, name: string, lead = 48, long = 150): Snippet {
  const hits: number[] = [];
  for (let i = text.indexOf(name); i >= 0; i = text.indexOf(name, i + 1)) {
    const before = text[i - 1];
    const after = text[i + name.length];
    if ((!before || !nameChar.test(before)) && (!after || !nameChar.test(after))) hits.push(i);
  }
  let start = 0;
  if (text.length > long && hits.length && hits[0] > lead) {
    start = hits[0] - lead;
    const space = text.indexOf(" ", start);
    if (space >= 0 && space < hits[0]) start = space + 1;
  }
  const parts: Snippet["parts"] = [];
  let at = start;
  for (const i of hits.filter((i) => i >= start)) {
    if (i > at) parts.push({ text: text.slice(at, i), hit: false });
    parts.push({ text: name, hit: true });
    at = i + name.length;
  }
  if (at < text.length) parts.push({ text: text.slice(at), hit: false });
  return { cut: start > 0, parts };
}
