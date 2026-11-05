// 关系图的两种排法：全图用力导向布局(有关系的技能靠近，没关系的分开);点了一个技能时只排它和上下游——
// 提到它的在左、它提到的在右、互相提到的在上。都是纯计算，不碰界面：同样的图每次排出一样的结果。
import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY, type SimulationNodeDatum } from "d3-force";
import type { SkillID } from "@bindings/internal/library";
import { relations, type GraphNode, type MentionGraph, type Relation } from "./graph";

export type XY = { x: number; y: number };
export type Positions = Map<SkillID, XY>;

// radius 是圆点的半径：被越多技能提到越大。
export const radius = (n: Pick<GraphNode, "mentioned">) => Math.min(24, 6 + 3.5 * Math.sqrt(n.mentioned));

// estimateWidth 是名字大约多宽(12.5px 的字);界面里有真实字体时用量出来的宽度。
export const estimateWidth = (name: string) => name.length * 6.9;

// forceLayout 是全图的位置。随机数用固定种子，先在后台把模拟跑完，打开时直接显示排好的结果、不在屏幕上晃动;
// 同一张图每次排出一样的位置。中心在 (0,0),由界面缩放平移放进框里。
export function forceLayout(graph: MentionGraph, width = estimateWidth): Positions {
  type Datum = SimulationNodeDatum & { id: SkillID; r: number; label: number };
  const n = graph.nodes.length;
  const nodes: Datum[] = graph.nodes.map((g) => ({ id: g.skill.id, r: radius(g), label: width(g.skill.name) }));
  const links = graph.links.map((l) => ({ source: l.from, target: l.to }));
  let seed = 7;
  const random = () => {
    seed = (seed * 1664525 + 1013904223) % 4294967296;
    return seed / 4294967296;
  };
  const sim = forceSimulation(nodes)
    .randomSource(random)
    .force(
      "link",
      forceLink<Datum, { source: SkillID; target: SkillID }>(links)
        .id((d) => d.id)
        .distance(60 + 4 * Math.sqrt(n))
        .strength(0.5),
    )
    .force("charge", forceManyBody().strength(-380 - n * 4))
    .force("collide", forceCollide<Datum>((d) => Math.max(d.r + 12, d.label / 2 + 8)).strength(0.9))
    .force("x", forceX(0).strength(0.08))
    .force("y", forceY(0).strength(0.12))
    .stop();
  sim.tick(400);
  return new Map(nodes.map((d) => [d.id, { x: d.x ?? 0, y: d.y ?? 0 }]));
}

const rowGap = 46;

// focusLayout 是点了 id 之后的位置：只有它和上下游。它在 (0,0);提到它的排在左边一列，它提到的排在右边一列，
// 一列超过 rows 个时再往外加一列;互相提到的排在上面一行。名字写在圆点右边，列与列之间按名字的宽度留出空。
export function focusLayout(graph: MentionGraph, id: SkillID, rows = 12, width = estimateWidth): { at: Positions; relation: Map<SkillID, Relation> } {
  const relation = relations(graph.links, id);
  const byID = new Map(graph.nodes.map((n) => [n.skill.id, n]));
  const name = (x: SkillID) => byID.get(x)?.skill.name ?? "";
  const group = (r: Relation) =>
    [...relation]
      .filter(([, v]) => v === r)
      .map(([k]) => k)
      .sort((a, b) => name(a).localeCompare(name(b)));
  const [ins, outs, both] = [group("in"), group("out"), group("both")];
  const at: Positions = new Map([[id, { x: 0, y: 0 }]]);
  const columns = (list: SkillID[]) => {
    const cols: SkillID[][] = [];
    for (let i = 0; i < list.length; i += rows) cols.push(list.slice(i, i + rows));
    return cols;
  };
  const labelRoom = (list: SkillID[]) => Math.max(110, ...list.map((x) => width(name(x)))) + 50;
  // 右边：从选中的技能名字右边开始。
  let x = Math.max(150, width(name(id)) + 70);
  for (const col of columns(outs)) {
    col.forEach((k, i) => at.set(k, { x, y: (i - (col.length - 1) / 2) * rowGap }));
    x += labelRoom(col);
  }
  // 左边：每列的名字写在圆点右边，伸向中间，所以列的位置要让出名字的宽度。
  x = 0;
  for (const col of columns(ins)) {
    x -= labelRoom(col) + 20;
    col.forEach((k, i) => at.set(k, { x, y: (i - (col.length - 1) / 2) * rowGap }));
  }
  // 上面：排在左右两边占的宽度以内，每行放得下几个放几个，往上一行行排;每行错开一点，免得指向上面一行的线穿过下面一行的圆点。
  const tallest = Math.min(rows, Math.max(ins.length, outs.length, 1));
  let y = -((tallest - 1) / 2) * rowGap - 80;
  const xs = [...at].filter(([k]) => k !== id).map(([k, p]) => [p.x, p.x + 20 + width(name(k))]);
  const left = Math.min(-40, ...xs.map(([a]) => a));
  const right = Math.max(width(name(id)) + 40, ...xs.map(([, b]) => b));
  const room = labelRoom(both);
  const perRow = Math.max(2, Math.floor((right - left) / room));
  for (let i = 0, k = 0; i < both.length; i += perRow, k++) {
    const row = both.slice(i, i + perRow);
    const start = (left + right) / 2 - (row.length * room) / 2 + ((k * 0.37) % 1) * room * 0.5;
    row.forEach((b, j) => at.set(b, { x: start + j * room, y }));
    y -= 56;
  }
  return { at, relation };
}
