// 关系图页：一个集合或组里的技能之间谁提到了谁。一个技能的 Markdown 文件里提到库里另一个技能时，画一条从它指向对方的线;
// 从左往右按提到的方向分层(ELK 的分层布局),没被提到的入口技能固定在最左一层。
// 悬停技能高亮它提到的和提到它的技能与相连的线，其他变淡;悬停线显示提到对方的那几行原文，点线把提示固定住、可以滚动看全部;
// 点技能打开详情抽屉。本集合或组以外被提到的技能用虚线框画出，写出它在哪个集合;和谁都没有连线的技能不进图，收在图下方一行。
// 快照变了(装了、写了备注、检查了更新)而技能和线没变时不重新排，视野也不动;技能或线变了才重新排，排好之前接着显示旧图。
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BaseEdge, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type EdgeProps, type Node, type NodeProps, type Viewport } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import ELK, { type ElkNode } from "elkjs/lib/elk.bundled.js";
import type { Mention, MentionsFilter, Skill, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { mentionsQuery, toLibraryError, useSnapshot } from "@/lib/library";
import { edgeKey, layoutKey, mentionGraph, mentionSnippet, neighbours, searchHits, type GraphNode, type MentionGraph } from "@/lib/graph";
import { matches, type GraphSet } from "@/lib/model";
import { cn } from "@/lib/utils";
import { toggleDrawer, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/app/icons";
import { SearchLine } from "@/components/app/skill-list/search-line";

const elk = new ELK();

// 节点里两行字的字号与字重：量宽和画出来用同一份，字体取界面实际用的 --font-sans。
const nameFont = { fontSize: 12.5, fontWeight: 600 };
const whereFont = { fontSize: 10.5, fontWeight: 400 };
const nodeHeight = 32;
const outsideHeight = 44;

let canvas: HTMLCanvasElement | undefined;
function textWidth(text: string, font: { fontSize: number; fontWeight: number }): number {
  canvas ??= document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx) return text.length * font.fontSize * 0.62;
  ctx.font = `${font.fontWeight} ${font.fontSize}px ${getComputedStyle(document.body).fontFamily}`;
  return ctx.measureText(text).width;
}

// nodeSize 是节点的大小：两边内边距 12、边框 1,再留几像素，免得字体渲染上的出入把名字截成省略号。
function nodeSize(n: GraphNode): { width: number; height: number } {
  const name = textWidth(n.skill.name, nameFont);
  const where = n.outside ? textWidth(n.where ?? "", whereFont) : 0;
  return { width: Math.ceil(Math.min(260, Math.max(name, where)) + 32), height: n.outside ? outsideHeight : nodeHeight };
}

type XY = { x: number; y: number };
type Placed = { nodes: Record<SkillID, XY & { width: number; height: number }>; edges: Record<string, XY[]> };

// layout 用 ELK 的分层布局排出每个技能的位置和每条线的走向(样条曲线，反向的线也由 ELK 绕好);
// 没被别的技能提到的固定在最左一层。
async function layout(graph: MentionGraph): Promise<Placed> {
  const mentioned = new Set(graph.edges.map((m) => m.to));
  const root: ElkNode = {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.edgeRouting": "SPLINES",
      "elk.layered.edgeRouting.splines.mode": "SLOPPY",
      "elk.layered.spacing.nodeNodeBetweenLayers": "44",
      "elk.layered.spacing.edgeNodeBetweenLayers": "14",
      "elk.layered.spacing.edgeEdgeBetweenLayers": "6",
      "elk.spacing.edgeEdge": "6",
      "elk.spacing.edgeNode": "12",
      "elk.spacing.nodeNode": "18",
      "elk.spacing.componentComponent": "36",
      "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
      "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
    },
    children: graph.nodes.map((n) => ({
      id: String(n.skill.id),
      ...nodeSize(n),
      layoutOptions: mentioned.has(n.skill.id) ? undefined : { "elk.layered.layering.layerConstraint": "FIRST" },
    })),
    edges: graph.edges.map((m) => ({ id: edgeKey(m), sources: [String(m.from)], targets: [String(m.to)] })),
  };
  const result = await elk.layout(root);
  const placed: Placed = { nodes: {}, edges: {} };
  for (const c of result.children ?? []) placed.nodes[Number(c.id)] = { x: c.x ?? 0, y: c.y ?? 0, width: c.width ?? 0, height: c.height ?? 0 };
  for (const e of result.edges ?? []) {
    const section = e.sections?.[0];
    if (section) placed.edges[e.id] = [section.startPoint, ...(section.bendPoints ?? []), section.endPoint];
  }
  return placed;
}

// splinePath 把 ELK 给的样条画出来:起点、终点之间每三个点是一段三次贝塞尔曲线的两个控制点和终点;对不上时退回圆角折线。
function splinePath(points: XY[]): string {
  if ((points.length - 1) % 3 !== 0) return roundedPath(points);
  let d = `M ${points[0].x} ${points[0].y}`;
  for (let i = 1; i < points.length; i += 3) {
    const [a, b, c] = [points[i], points[i + 1], points[i + 2]];
    d += ` C ${a.x} ${a.y} ${b.x} ${b.y} ${c.x} ${c.y}`;
  }
  return d;
}

// roundedPath 把折线画成拐角带圆角的路径。
function roundedPath(points: XY[], radius = 8): string {
  let d = `M ${points[0].x} ${points[0].y}`;
  for (let i = 1; i < points.length - 1; i++) {
    const [a, b, c] = [points[i - 1], points[i], points[i + 1]];
    const r = Math.min(radius, Math.hypot(b.x - a.x, b.y - a.y) / 2, Math.hypot(c.x - b.x, c.y - b.y) / 2);
    const toward = (p: XY, q: XY) => {
      const len = Math.hypot(q.x - p.x, q.y - p.y) || 1;
      return { x: p.x + ((q.x - p.x) / len) * r, y: p.y + ((q.y - p.y) / len) * r };
    };
    const before = toward(b, a);
    const after = toward(b, c);
    d += ` L ${before.x} ${before.y} Q ${b.x} ${b.y} ${after.x} ${after.y}`;
  }
  const last = points[points.length - 1];
  return `${d} L ${last.x} ${last.y}`;
}

type RoutedEdge = Edge<{ points: XY[] }, "routed">;

function Routed({ id, data, style, markerEnd, interactionWidth }: EdgeProps<RoutedEdge>) {
  if (!data?.points.length) return null;
  return <BaseEdge id={id} path={splinePath(data.points)} style={style} markerEnd={markerEnd} interactionWidth={interactionWidth} />;
}

const edgeTypes = { routed: Routed };

// MentionGraphPage 是关系图页的内容：members 是这个集合或组里的技能。
export function MentionGraphPage({ set, members }: { set: GraphSet; members: SkillID[] }) {
  const t = useT().graph;
  const filter: MentionsFilter = set.kind === "collection" ? { collection_id: set.id } : { group_id: set.id };
  const query = useQuery(mentionsQuery(filter));
  if (query.isError) {
    return (
      <Centered>
        <b className="text-sm text-foreground">{t.failed}</b>
        <span className="select-text">{toLibraryError(query.error).message}</span>
        <Button className="mt-1.5" onClick={() => void query.refetch()}>
          {t.retry}
        </Button>
      </Centered>
    );
  }
  if (!query.data) return <Centered>{t.reading}</Centered>;
  return <Loaded members={members} mentions={query.data} group={set.kind === "group"} />;
}

function Loaded({ members, mentions, group }: { members: SkillID[]; mentions: Mention[]; group: boolean }) {
  const s = useSnapshot();
  const tt = useT();
  const t = tt.graph;
  const search = useView((v) => v.search);
  const graph = useMemo(() => mentionGraph(s, members, mentions), [s, members, mentions]);
  const outside = graph.nodes.filter((n) => n.outside).length;
  return (
    <div className="graph-page flex min-h-0 flex-1 flex-col gap-2 px-3.5 pt-3 pb-3">
      <div className="[&>.search-line]:mb-0">
        <SearchLine results={searchHits(graph, search)} />
      </div>
      <div className="graph-summary flex min-w-0 items-center gap-2 text-[11.5px] text-muted-foreground">
        <span className="shrink-0 font-medium text-foreground-secondary">{t.summary(members.length, graph.edges.length, outside, group)}</span>
        <span className="min-w-0 truncate">{t.hint}</span>
      </div>
      {graph.nodes.length ? (
        <Canvas graph={graph} />
      ) : (
        <div className="graph-empty flex min-h-0 flex-1 flex-col items-center justify-center gap-1 rounded-[10px] border border-hairline bg-white/55 text-[12px] text-muted-foreground shadow-tile">
          {members.length ? (
            t.none
          ) : (
            <>
              <b className="font-medium text-foreground-secondary">{tt.list.emptyGroup}</b>
              <span>{tt.list.emptyGroupHint}</span>
            </>
          )}
        </div>
      )}
      {graph.loose.length > 0 && <Loose skills={graph.loose} />}
    </div>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-col items-center justify-center gap-1.5 pt-[22vh] text-[12px] text-muted-foreground">{children}</div>;
}

// Loose 是图下方的一行：和这里的技能都没有连线的，点了同样打开详情。
function Loose({ skills }: { skills: Skill[] }) {
  const t = useT().graph;
  const search = useView((v) => v.search);
  return (
    <div className="graph-loose flex max-h-[30%] min-w-0 flex-wrap items-center gap-1.5 overflow-y-auto text-[11.5px] text-muted-foreground">
      <span className="shrink-0">{t.loose(skills.length)}</span>
      {skills.map((x) => (
        <button
          key={x.id}
          type="button"
          className={cn(
            "graph-loose-skill rounded-md border border-hairline bg-white/70 px-2 py-0.5 font-medium text-foreground-secondary hover:border-primary-border hover:text-primary-strong",
            !matches(search, x) && "opacity-35",
          )}
          onClick={() => toggleDrawer(x.id)}
          title={x.description}
        >
          {x.name}
        </button>
      ))}
    </div>
  );
}

type SkillNodeData = { node: GraphNode; dim: boolean; lit: boolean; open: boolean };
type SkillFlowNode = Node<SkillNodeData, "skill">;

const SkillNode = memo(function SkillNode({ data }: NodeProps<SkillFlowNode>) {
  const { node, dim, lit, open } = data;
  return (
    <div
      className={cn(
        "graph-node flex h-full w-full cursor-pointer flex-col justify-center rounded-lg border px-3 transition-opacity",
        node.outside ? "border-dashed border-border-strong bg-white/60" : "border-hairline bg-white shadow-tile",
        lit && "border-primary-border bg-primary-soft",
        open && "ring-2 ring-primary/40",
        dim && "opacity-25",
      )}
      data-skill={node.skill.name}
      data-outside={node.outside || undefined}
    >
      <Handle type="target" position={Position.Left} className="!size-1 !min-h-0 !min-w-0 !border-0 !bg-transparent" isConnectable={false} />
      <span className={cn("truncate", node.outside ? "text-foreground-secondary" : "text-foreground", lit && "text-primary-strong")} style={nameFont}>
        {node.skill.name}
      </span>
      {node.outside && (
        <span className="graph-node-where truncate text-muted-foreground" style={whereFont}>
          {node.where}
        </span>
      )}
      <Handle type="source" position={Position.Right} className="!size-1 !min-h-0 !min-w-0 !border-0 !bg-transparent" isConnectable={false} />
    </div>
  );
});

const nodeTypes = { skill: SkillNode };

// Tip 是线的提示：跟着鼠标(悬停)或固定住(点了线)。
interface Tip {
  key: string;
  x: number;
  y: number;
  pinned: boolean;
}

// Canvas 是图本身：先按 ELK 排出位置，再交给 React Flow 画;可以拖动平移、捏合缩放。
function Canvas({ graph }: { graph: MentionGraph }) {
  const search = useView((v) => v.search);
  const drawer = useView((v) => v.drawer);
  const [placed, setPlaced] = useState<{ key: string; at: Placed; initial: Viewport } | null>(null);
  const [hoverNode, setHoverNode] = useState<SkillID | null>(null);
  const [tip, setTip] = useState<Tip | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const latest = useRef(graph);
  latest.current = graph;
  const key = useMemo(() => layoutKey(graph), [graph]);

  useEffect(() => {
    let live = true;
    void layout(latest.current).then((at) => {
      if (live) setPlaced((p) => ({ key, at, initial: p?.initial ?? initialViewport(at, box.current) }));
    });
    return () => {
      live = false;
    };
  }, [key]);

  // 固定住的提示按 Esc 关掉。
  useEffect(() => {
    if (!tip?.pinned) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setTip(null);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [tip?.pinned]);

  const byKey = useMemo(() => new Map(graph.edges.map((m) => [edgeKey(m), m])), [graph]);
  const focus = useMemo(() => {
    if (hoverNode !== null) return neighbours(graph.edges, hoverNode);
    const m = tip && byKey.get(tip.key);
    if (m) return { nodes: new Set([m.from, m.to]), edges: new Set([tip.key]) };
    return null;
  }, [graph, hoverNode, tip, byKey]);

  const searching = !!search.trim();
  const nodes = useMemo<SkillFlowNode[]>(() => {
    if (!placed) return [];
    return graph.nodes.flatMap((n) => {
      const p = placed.at.nodes[n.skill.id];
      if (!p) return [];
      const lit = !!focus?.nodes.has(n.skill.id);
      const dim = focus ? !lit : searching && !matches(search, n.skill);
      return [
        {
          id: String(n.skill.id),
          type: "skill" as const,
          position: { x: p.x, y: p.y },
          width: p.width,
          height: p.height,
          // 直接给出两端的连接点：悬停时节点对象换新,React Flow 不必重新量，连线也就不会被拆掉重建(重建会让点线的 click 丢掉)。
          handles: [
            { type: "target" as const, position: Position.Left, x: 0, y: p.height / 2 },
            { type: "source" as const, position: Position.Right, x: p.width, y: p.height / 2 },
          ],
          data: { node: n, dim, lit, open: drawer === n.skill.id },
        },
      ];
    });
  }, [graph, placed, focus, search, searching, drawer]);

  const edges = useMemo<RoutedEdge[]>(
    () =>
      graph.edges.flatMap((m) => {
        const key = edgeKey(m);
        const points = placed?.at.edges[key];
        if (!points) return [];
        const lit = !!focus?.edges.has(key);
        const color = lit ? "var(--color-primary)" : "var(--color-subtle)";
        return [
          {
            id: key,
            source: String(m.from),
            target: String(m.to),
            type: "routed" as const,
            data: { points },
            interactionWidth: 18,
            markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color },
            style: { stroke: color, strokeWidth: lit ? 1.8 : 1.2, opacity: focus && !lit ? 0.12 : 1, transition: "opacity .12s" },
          },
        ];
      }),
    [graph, focus, placed],
  );

  const at = (e: React.MouseEvent) => {
    const r = box.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };
  const hover = (e: React.MouseEvent, edge: Edge) => setTip((t) => (t?.pinned ? t : { key: edge.id, ...at(e), pinned: false }));
  const tipMention = tip && byKey.get(tip.key);

  return (
    <div ref={box} className="graph-canvas relative min-h-0 flex-1 overflow-hidden rounded-[10px] border border-hairline bg-white/55 shadow-tile">
      {placed && (
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          defaultViewport={placed.initial}
          minZoom={0.2}
          maxZoom={2}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          panOnScroll
          zoomOnScroll={false}
          zoomOnPinch
          proOptions={{ hideAttribution: true }}
          onNodeMouseEnter={(_, n) => {
            setTip((t) => (t?.pinned ? t : null));
            setHoverNode(Number(n.id));
          }}
          onNodeMouseLeave={() => setHoverNode(null)}
          onNodeClick={(_, n) => {
            setTip(null);
            toggleDrawer(Number(n.id));
          }}
          onPaneClick={() => setTip(null)}
          onMove={() => setTip((t) => (t?.pinned ? t : null))}
          onPaneMouseLeave={() => {
            setTip((t) => (t?.pinned ? t : null));
            setHoverNode(null);
          }}
          onEdgeClick={(e, edge) => setTip({ key: edge.id, ...at(e), pinned: true })}
          onEdgeMouseEnter={hover}
          onEdgeMouseMove={hover}
          onEdgeMouseLeave={() => setTip((t) => (t?.pinned ? t : null))}
        >
          {/* 放在左下角：右侧会被详情抽屉盖住。 */}
          <Controls showInteractive={false} position="bottom-left" fitViewOptions={{ padding: 0.08, maxZoom: 1 }} />
        </ReactFlow>
      )}
      {tip && tipMention && <LinesTip mention={tipMention} tip={tip} box={box.current} onClose={() => setTip(null)} />}
    </div>
  );
}

// initialViewport 是图第一次画出来时的视野：放得下就整张缩放到框里(不放大);太大时不缩到字看不清，
// 从最左边(入口技能)开始显示，其余拖动或滚动查看。之后重新排时保留用户当时的视野。
function initialViewport(placed: Placed, box: HTMLDivElement | null): Viewport {
  const all = Object.values(placed.nodes);
  if (!box || !all.length) return { x: 0, y: 0, zoom: 1 };
  const pad = 24;
  const right = Math.max(...all.map((n) => n.x + n.width));
  const bottom = Math.max(...all.map((n) => n.y + n.height));
  const [w, h] = [box.clientWidth - pad * 2, box.clientHeight - pad * 2];
  const zoom = Math.max(0.8, Math.min(1, w / right, h / bottom));
  const x = right * zoom <= w ? (box.clientWidth - right * zoom) / 2 : pad;
  const y = bottom * zoom <= h ? (box.clientHeight - bottom * zoom) / 2 : pad;
  return { x, y, zoom };
}

const maxLines = 6;

// LinesTip 是线的提示：谁提到谁，以及提到对方的那几行原文，被提到的名字高亮;长行从名字前面一点开始显示。
// 悬停时跟着鼠标、最多 6 行、每行最多 3 行高;点了线固定住，列出全部行和整行原文，可以滚动，点空白处、× 或 Esc 关掉。
function LinesTip({ mention, tip, box, onClose }: { mention: Mention; tip: Tip; box: HTMLDivElement | null; onClose: () => void }) {
  const s = useSnapshot();
  const t = useT().graph;
  const name = (id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;
  const target = name(mention.to);
  const lines = mention.lines ?? [];
  const shown = tip.pinned ? lines : lines.slice(0, maxLines);
  const width = 400;
  const right = box ? tip.x + 16 + width > box.clientWidth : false;
  const below = box ? tip.y < box.clientHeight / 2 : true;
  return (
    <div
      className={cn(
        "graph-tip absolute z-10 flex w-[400px] flex-col gap-1.5 glass glass-clear rounded-lg border px-3 py-2.5 text-[11.5px] leading-[1.45] text-foreground [--glass-drop:var(--shadow-popover)]",
        tip.pinned ? "max-h-[60%] overflow-y-auto select-text" : "pointer-events-none",
      )}
      data-pinned={tip.pinned || undefined}
      style={{
        left: right ? undefined : tip.x + 16,
        right: right && box ? box.clientWidth - tip.x + 16 : undefined,
        top: below ? tip.y + 14 : undefined,
        bottom: below || !box ? undefined : box.clientHeight - tip.y + 14,
      }}
    >
      <div className="flex items-start gap-2">
        <b className="min-w-0 grow text-[12px] font-semibold">{t.mentions(name(mention.from), target)}</b>
        {tip.pinned && (
          <button type="button" className="graph-tip-close shrink-0 text-muted-foreground hover:text-foreground" title={t.close} onClick={onClose}>
            <Icon name="close" className="size-3" />
          </button>
        )}
      </div>
      {shown.map((l, i) => {
        const snip = tip.pinned ? mentionSnippet(l.text, target, 0, Infinity) : mentionSnippet(l.text, target);
        return (
          <div key={i} className="graph-tip-line min-w-0">
            <span className="font-mono text-[10px] text-subtle">{l.path}</span>
            <p className={cn("break-words text-foreground-secondary", !tip.pinned && "line-clamp-3")}>
              {snip.cut && "…"}
              {snip.parts.map((p, j) =>
                p.hit ? (
                  <mark key={j} className="rounded-[3px] bg-primary-soft px-0.5 font-semibold text-primary-strong">
                    {p.text}
                  </mark>
                ) : (
                  <span key={j}>{p.text}</span>
                ),
              )}
            </p>
          </div>
        );
      })}
      {!tip.pinned && lines.length > maxLines && <span className="text-muted-foreground">{t.moreLines(lines.length - maxLines)}</span>}
    </div>
  );
}
