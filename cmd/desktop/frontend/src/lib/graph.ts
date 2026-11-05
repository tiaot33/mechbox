// 关系图要画什么：集合或组里的技能、它们提到的本集合或组以外的技能，以及谁提到谁;没有连线的技能不进图，收在图下方。
// 只是把技能库给出的「提到」关系整理成圆点与连线，不碰界面;布局在 graph-layout.ts,画在 components/app/mention-graph 里。
import type { CollectionID, Mention, Skill, SkillID } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { collator, matches, sourceLabel } from "./model";

// GraphNode 是图上的一个技能。outside 的是本集合或组以外的技能(画成空心虚线圆),where 写它在哪个集合(没有来源的写「没有来源」);
// collection 是它所在的集合(按集合着色，没有来源的是 null);mentioned 是图里有几个技能提到它(决定圆点大小)。
export interface GraphNode {
  skill: Skill;
  outside: boolean;
  where?: string;
  collection: CollectionID | null;
  mentioned: number;
}

// GraphLink 是两个技能之间的一条线：from 提到 to;both 时 to 也提到 from,线两头都有箭头。
// mentions 是这条线代表的提到关系(一条或两条),悬停时显示它们的原文。
export interface GraphLink {
  key: string;
  from: SkillID;
  to: SkillID;
  both: boolean;
  mentions: Mention[];
}

// MentionGraph 是一张关系图:nodes 按名字排,edges 是技能库给出的提到关系(两头都在 nodes 里),
// links 是画出来的线(互相提到的两条合成一条),loose 是本集合或组里和谁都没有连线的技能，按名字排。
export interface MentionGraph {
  nodes: GraphNode[];
  edges: Mention[];
  links: GraphLink[];
  loose: Skill[];
}

export function mentionGraph(s: Snapshot, members: SkillID[], mentions: Mention[]): MentionGraph {
  const byID = new Map(s.skills.map((x) => [x.id, x]));
  const inSet = new Set(members);
  const edges = mentions.filter((m) => m.from !== m.to && inSet.has(m.from) && byID.has(m.from) && byID.has(m.to));
  const linked = new Set(edges.flatMap((m) => [m.from, m.to]));
  const owner = new Map<SkillID, CollectionID>();
  for (const c of s.collections) for (const id of c.skills ?? []) owner.set(id, c.id);
  const mentioned = new Map<SkillID, number>();
  for (const m of edges) mentioned.set(m.to, (mentioned.get(m.to) ?? 0) + 1);
  const byName = (a: Skill, b: Skill) => collator.compare(a.name, b.name) || a.id - b.id;
  const nodes = [...linked]
    .map((id) => byID.get(id)!)
    .sort(byName)
    .map(
      (skill): GraphNode => ({
        skill,
        outside: !inSet.has(skill.id),
        ...(!inSet.has(skill.id) && { where: sourceLabel(s, skill.source) }),
        collection: owner.get(skill.id) ?? null,
        mentioned: mentioned.get(skill.id) ?? 0,
      }),
    );
  const loose = members
    .map((id) => byID.get(id))
    .filter((x): x is Skill => !!x && !linked.has(x.id))
    .sort(byName);
  return { nodes, edges, links: graphLinks(edges), loose };
}

// graphLinks 把提到关系合成线：同一对技能互相提到时只留一条，两头都有箭头。
function graphLinks(edges: Mention[]): GraphLink[] {
  const byPair = new Map<string, GraphLink>();
  for (const m of edges) {
    const key = pairKey(m.from, m.to);
    const link = byPair.get(key);
    if (link) {
      link.both = true;
      link.mentions.push(m);
    } else byPair.set(key, { key, from: m.from, to: m.to, both: false, mentions: [m] });
  }
  return [...byPair.values()];
}

export const pairKey = (a: SkillID, b: SkillID) => (a < b ? `${a}-${b}` : `${b}-${a}`);

// layoutKey 是决定布局的那部分：哪些技能(名字、是否在外面、在哪个集合)、哪些线。只有它变了才重新排;
// 快照别的地方变了(装了、写了备注、检查了更新)图不动。
export function layoutKey(graph: MentionGraph): string {
  return JSON.stringify([graph.nodes.map((n) => [n.skill.id, n.skill.name, n.outside, n.where ?? "", n.collection]), graph.edges.map(edgeKey)]);
}

// searchHits 是关系图页搜索的结果个数：图里的技能(包括虚线圆)与图下方没有连线的技能，名字或描述匹配的。
export function searchHits(graph: MentionGraph, search: string): number {
  return graph.nodes.filter((n) => matches(search, n.skill)).length + graph.loose.filter((x) => matches(search, x)).length;
}

// neighbours 是悬停一个技能时要高亮的：它自己、它提到的和提到它的技能，以及相连的线(按 GraphLink 的 key)。
export function neighbours(links: GraphLink[], id: SkillID): { nodes: Set<SkillID>; links: Set<string> } {
  const nodes = new Set<SkillID>([id]);
  const keys = new Set<string>();
  for (const l of links) {
    if (l.from !== id && l.to !== id) continue;
    nodes.add(l.from);
    nodes.add(l.to);
    keys.add(l.key);
  }
  return { nodes, links: keys };
}

// Relation 是点一个技能后，上下游里的技能和它的关系：提到它的(in)、它提到的(out)、互相提到的(both)。
export type Relation = "in" | "out" | "both";

// relations 是点了 id 之后图上留下的上下游：技能 → 和 id 的关系。
export function relations(links: GraphLink[], id: SkillID): Map<SkillID, Relation> {
  const out = new Map<SkillID, Relation>();
  for (const l of links) {
    if (l.from !== id && l.to !== id) continue;
    const other = l.from === id ? l.to : l.from;
    out.set(other, l.both ? "both" : l.from === id ? "out" : "in");
  }
  return out;
}

// labelled 是不悬停、不放大时显示名字的技能：不超过 30 个技能时全部显示;更多时只显示被提到最多的约 15 个
// (和第 15 个提到次数一样多的也显示)。
export function labelled(nodes: GraphNode[], all = 30, top = 15): Set<SkillID> {
  if (nodes.length <= all) return new Set(nodes.map((n) => n.skill.id));
  const counts = nodes.map((n) => n.mentioned).sort((a, b) => b - a);
  const least = Math.max(1, counts[top - 1]);
  return new Set(nodes.filter((n) => n.mentioned >= least).map((n) => n.skill.id));
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
